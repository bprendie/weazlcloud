package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/vault"
	"time"
)

const photoPreparationStateLimit = 16 << 10

var ErrPhotoPreparationAction = errors.New("photo preparation action must be start, resume, pause, or retry")

type photoPreparation struct {
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	Total         int    `json:"total"`
	Ready         int    `json:"ready"`
	BundleReady   int    `json:"bundle_ready"`
	Failed        int    `json:"failed"`
	Position      int    `json:"position"`
	Generation    uint64 `json:"generation"`
	Updated       string `json:"updated"`
	Paused        bool   `json:"paused,omitempty"`
	Retry         bool   `json:"retry,omitempty"`
	AutoOnly      bool   `json:"auto_only,omitempty"`
	Error         string `json:"error,omitempty"`
	CPUBudget     int    `json:"cpu_budget,omitempty"`
	Workers       int    `json:"workers,omitempty"`
	Readers       int    `json:"source_readers,omitempty"`
	Schedule      string `json:"schedule,omitempty"`
	Working       int    `json:"working,omitempty"`
	WorkProgress  int    `json:"work_progress,omitempty"`
	CacheChecking bool   `json:"cache_checking,omitempty"`
}

func (l *Library) PhotoPreparation() (photoPreparation, error) {
	if !l.vault.Unlocked() {
		return photoPreparation{}, vault.ErrLocked
	}
	l.photoPrepMu.Lock()
	defer l.photoPrepMu.Unlock()
	l.loadPhotoPreparationLocked()
	l.reconcilePreparedCacheLocked()
	state := l.photoPrep
	state.CacheChecking = l.photoCacheChecking
	state.CPUBudget, state.Workers, state.Readers, state.Schedule = previewPolicy.CPUBudget, previewPolicy.RenderWorkers, previewPolicy.SourceReaders, previewPolicy.Schedule
	l.photoPrepMu.Unlock()
	state.Working, state.WorkProgress = l.photoJobProgress()
	l.photoPrepMu.Lock()
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
		return photoPreparation{}, vault.ErrLocked
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
		l.photoPrep.AutoOnly = false
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
		promoteAutoOnly := action == "resume" && l.photoPrep.AutoOnly
		// An explicit resume from the Photos UI means finish the entire library.
		// AutoOnly is reserved for background ingestion and otherwise leaves a
		// completed upload queue with no pending jobs to lease.
		if action == "retry" || action == "resume" {
			l.photoPrep.AutoOnly = false
		}
		l.photoPrep.Enabled = true
		l.photoPrep.Status = "queued"
		if l.photoPrep.Generation != l.photoGeneration() || promoteAutoOnly {
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
	enabled := l.photoPrep.Enabled && !l.photoPrep.Paused && l.photoPrep.Status != "complete" && l.photoPrep.Status != "partial" && l.photoPrep.Status != "paused_error" && !(previewPolicy.Schedule == "quiet" && l.photoPrep.Status == "paused_schedule")
	l.photoPrepMu.Unlock()
	if enabled {
		l.startPhotoPreparationWorker()
	}
}

// ResumePhotoPreparation continues an explicitly enabled job after the vault
// unlocks, building the owner index first when this is a fresh process.
func (l *Library) ResumePhotoPreparation(_ context.Context) {
	l.ResumeLivePhotos()
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
			recovered, recoveryErr := l.RecoverHEICPreviews(workCtx)
			if recoveryErr != nil {
				return
			}
			expanded, recoveryErr := l.RecoverExpandedPreviews(workCtx)
			if recoveryErr != nil {
				return
			}
			if recovered+expanded > 0 {
				l.photoPrepMu.Lock()
				if !l.photoPrep.Paused {
					l.photoPrep.Status = "queued"
					_ = l.savePhotoPreparationLocked()
				}
				l.photoPrepMu.Unlock()
			}
			l.resumePhotoPreparation()
		}
	}()
}

func (l *Library) photoCount() int {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	n := 0
	for _, f := range l.photoRows {
		if !f.Folder && photoPreviewable(f.Path) {
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

// PrepareVaultLock cancels and drains background photo work while the vault
// key remains available to release durable queue leases safely.
func (l *Library) PrepareVaultLock() {
	l.photoPrepMu.Lock()
	if !l.photoPrepRunning {
		l.photoPrepMu.Unlock()
		return
	}
	l.photoStoppingForLock = true
	if l.photoPrepCancel != nil {
		l.photoPrepCancel()
	}
	l.photoPrepMu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		l.photoPrepMu.Lock()
		running := l.photoPrepRunning
		if !running {
			l.photoStoppingForLock = false
		}
		l.photoPrepMu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
