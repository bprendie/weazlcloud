package library

import (
	"context"
	"errors"
	"time"
)

const photoPreparationStateLimit = 16 << 10

var ErrPhotoPreparationAction = errors.New("photo preparation action must be start, resume, pause, or retry")

type photoPreparation struct {
	Enabled    bool   `json:"enabled"`
	Status     string `json:"status"`
	Total      int    `json:"total"`
	Ready      int    `json:"ready"`
	Failed     int    `json:"failed"`
	Position   int    `json:"position"`
	Generation uint64 `json:"generation"`
	Updated    string `json:"updated"`
	Paused     bool   `json:"paused,omitempty"`
	Retry      bool   `json:"retry,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (l *Library) PhotoPreparation() (photoPreparation, error) {
	if !l.vault.Unlocked() {
		return photoPreparation{}, errors.New("vault is locked")
	}
	l.photoPrepMu.Lock()
	defer l.photoPrepMu.Unlock()
	l.loadPhotoPreparationLocked()
	l.reconcilePreparedCacheLocked()
	state := l.photoPrep
	if state.Status == "paused_storage" && !l.photoStorageBusy() {
		go l.startPhotoPreparationWorker()
	}
	return state, nil
}

func (l *Library) SetPhotoPreparation(ctx context.Context, action string) (photoPreparation, error) {
	if action != "start" && action != "resume" && action != "pause" && action != "retry" {
		return photoPreparation{}, ErrPhotoPreparationAction
	}
	l.mu.Lock()
	if !l.vault.Unlocked() {
		l.mu.Unlock()
		return photoPreparation{}, errors.New("vault is locked")
	}
	if action != "pause" {
		if err := l.ensurePhotoIndexLocked(ctx); err != nil {
			l.mu.Unlock()
			return photoPreparation{}, err
		}
	}
	l.mu.Unlock()
	l.photoPrepMu.Lock()
	l.loadPhotoPreparationLocked()
	l.photoPrep.Paused = action == "pause"
	if l.photoPrepCancel != nil {
		l.photoPrepCancel()
	}
	l.photoPrep.Error = ""
	if action == "retry" {
		l.photoPrep.Retry = true
	}
	if action == "start" {
		l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
		l.photoPrep.Total = l.photoCount()
		l.photoPrep.Generation = l.photoGeneration()
		l.photoPrep.Enabled = true
		l.photoPrep.Status = "queued"
	} else if action == "pause" {
		if l.photoPrepCancel != nil {
			l.photoPrepCancel()
		}
		l.photoPrep.Enabled = true
		l.photoPrep.Status = "paused"
	} else {
		l.photoPrep.Enabled = true
		l.photoPrep.Status = "queued"
		if l.photoPrep.Generation != l.photoGeneration() {
			l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
			l.photoPrep.Total = l.photoCount()
			l.photoPrep.Generation = l.photoGeneration()
		}
	}
	l.touchPhotoPreparationLocked()
	err := l.savePhotoPreparationLocked()
	state := l.photoPrep
	l.photoPrepMu.Unlock()
	if action != "pause" && err == nil {
		l.startPhotoPreparationWorker()
	}
	return state, err
}

func (l *Library) resumePhotoPreparation() {
	l.photoPrepMu.Lock()
	l.loadPhotoPreparationLocked()
	if l.photoPrep.Enabled && l.photoPrep.Generation != l.photoGeneration() {
		l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
		l.photoPrep.Total = l.photoCount()
		l.photoPrep.Generation = l.photoGeneration()
		if !l.photoPrep.Paused {
			l.photoPrep.Status = "queued"
		}
		l.touchPhotoPreparationLocked()
		l.savePhotoPreparationLocked()
	}
	enabled := l.photoPrep.Enabled && !l.photoPrep.Paused && l.photoPrep.Status != "complete" && l.photoPrep.Status != "partial" && l.photoPrep.Status != "paused_error"
	l.photoPrepMu.Unlock()
	if enabled {
		l.startPhotoPreparationWorker()
	}
}

// ResumePhotoPreparation continues an explicitly enabled job after the vault
// unlocks, building the owner index first when this is a fresh process.
func (l *Library) ResumePhotoPreparation(_ context.Context) {
	if !l.vault.Unlocked() {
		return
	}
	workCtx, release := l.previewContext(context.Background())
	l.photoPrepMu.Lock()
	l.loadPhotoPreparationLocked()
	enabled := l.photoPrep.Enabled
	if !enabled || l.photoPrepRunning || l.photoResumeWaiting || workCtx.Err() != nil {
		l.photoPrepMu.Unlock()
		release()
		return
	}
	l.photoResumeWaiting = true
	l.photoPrepMu.Unlock()
	go func() {
		defer release()
		defer func() {
			l.photoPrepMu.Lock()
			l.photoResumeWaiting = false
			l.photoPrepMu.Unlock()
		}()
		l.mu.Lock()
		err := l.ensurePhotoIndexLocked(workCtx)
		l.mu.Unlock()
		if err == nil && workCtx.Err() == nil {
			l.resumePhotoPreparation()
		}
	}()
}

func (l *Library) photoCount() int {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	n := 0
	for _, f := range l.photoRows {
		if !f.Folder && photoRaster(f.Path) {
			n++
		}
	}
	return n
}

func (l *Library) startPhotoPreparationWorker() {
	ctx, release := l.previewContext(context.Background())
	if ctx.Err() != nil {
		release()
		return
	}
	l.photoPrepMu.Lock()
	if l.photoPrepRunning || !l.photoPrep.Enabled || l.photoPrep.Paused {
		l.photoPrepMu.Unlock()
		release()
		return
	}
	l.photoPrepRunning = true
	ctx, cancel := context.WithCancel(ctx)
	l.photoPrepCancel = cancel
	l.photoPrep.Status = "running"
	l.touchPhotoPreparationLocked()
	if l.savePhotoPreparationLocked() != nil {
		cancel()
		release()
		l.photoPrepRunning = false
		l.photoPrepMu.Unlock()
		return
	}
	l.photoPrepMu.Unlock()
	go func() { defer release(); defer cancel(); l.runPhotoPreparationParallel(ctx) }()
}

func (l *Library) photoStorageBusy() bool {
	l.stageMu.Lock()
	defer l.stageMu.Unlock()
	return len(l.activeStages) > 0 || l.photoImports > 0
}

func (l *Library) waitForPhotoStorage() {
	l.photoPrepMu.Lock()
	if l.photoResumeWaiting {
		l.photoPrepMu.Unlock()
		return
	}
	l.photoResumeWaiting = true
	l.photoPrepMu.Unlock()
	go func() {
		ctx, release := l.previewContext(context.Background())
		defer release()
		defer func() {
			l.photoPrepMu.Lock()
			l.photoResumeWaiting = false
			l.photoPrepMu.Unlock()
		}()
		for {
			if ctx.Err() != nil {
				return
			}
			l.photoPrepMu.Lock()
			running := l.photoPrepRunning
			l.photoPrepMu.Unlock()
			if !running && !l.photoStorageBusy() {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		l.startPhotoPreparationWorker()
	}()
}

func (l *Library) restartPhotoPreparation() {
	l.photoPrepMu.Lock()
	l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
	l.photoPrep.Total = l.photoCount()
	l.photoPrep.Generation = l.photoGeneration()
	l.touchPhotoPreparationLocked()
	l.savePhotoPreparationLocked()
	l.photoPrepMu.Unlock()
}

func (l *Library) finishPhotoPreparation(status string) {
	l.photoPrepMu.Lock()
	if !l.photoPrep.Paused && l.photoPrep.Status != "queued" {
		l.photoPrep.Status = status
	}
	l.touchPhotoPreparationLocked()
	l.savePhotoPreparationLocked()
	l.photoPrepMu.Unlock()
}

func (l *Library) touchPhotoPreparationLocked() {
	l.photoPrep.Updated = time.Now().UTC().Format(time.RFC3339)
}
