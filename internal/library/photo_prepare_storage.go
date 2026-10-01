package library

import (
	"context"
	"time"
)

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
