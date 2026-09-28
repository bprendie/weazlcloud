package library

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type photoPreparationResult struct {
	file catalog.File
	err  error
}

// Each pass has a stable snapshot. A restart scans identities again, using
// authenticated cache entries and durable failures instead of slice positions.
func (l *Library) runPhotoPreparationParallel(parent context.Context) {
	ctx := parent
	defer func() {
		l.photoPrepMu.Lock()
		l.photoPrepRunning = false
		l.photoPrepCancel = nil
		restart := !l.photoPrep.Paused && (l.photoPrep.Status == "queued" || (l.photoPrep.Status == "paused_locked" && l.vault.Unlocked()))
		l.photoPrepMu.Unlock()
		if restart {
			l.startPhotoPreparationWorker()
		}
	}()
	for ctx.Err() == nil {
		if l.photoStorageBusy() {
			l.finishPhotoPreparation("paused_storage")
			l.waitForPhotoStorage()
			return
		}
		l.photoMu.Lock()
		generation := l.photoEpoch
		files := make([]catalog.File, 0, len(l.photoMediaRows))
		for _, f := range l.photoMediaRows {
			if photoRaster(f.Path) {
				files = append(files, f)
			}
		}
		l.photoMu.Unlock()
		sort.Slice(files, func(i, j int) bool { return files[i].EntryID < files[j].EntryID })
		l.photoPrepMu.Lock()
		if l.photoPrep.Paused {
			l.photoPrepMu.Unlock()
			return
		}
		retry := l.photoPrep.Retry
		l.photoPrep.Generation, l.photoPrep.Total = generation, len(files)
		l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
		l.photoPrepMu.Unlock()
		for pos := 0; pos < len(files); {
			if ctx.Err() != nil {
				break
			}
			if l.photoStorageBusy() {
				l.finishPhotoPreparation("paused_storage")
				l.waitForPhotoStorage()
				return
			}
			if generation != l.photoGeneration() {
				break
			}
			end := min(pos+max(1, previewPolicy.BackgroundWorkers), len(files))
			results := make(chan photoPreparationResult, end-pos)
			for _, f := range files[pos:end] {
				go func(f catalog.File) { results <- photoPreparationResult{f, l.preparePhoto(ctx, f, retry)} }(f)
			}
			ready, failed := 0, 0
			var checkpointErr error
			for i := pos; i < end; i++ {
				result := <-results
				if errors.Is(result.err, errPhotoFailureRecord) {
					checkpointErr = result.err
				}
				if result.err == nil {
					ready++
				} else {
					failed++
				}
			}
			l.photoPrepMu.Lock()
			if ctx.Err() != nil || l.photoPrep.Paused {
				l.photoPrepMu.Unlock()
				break
			}
			if checkpointErr != nil {
				l.photoPrep.Status = "paused_error"
				l.photoPrep.Error = checkpointErr.Error()
				l.photoPrepMu.Unlock()
				return
			}
			l.photoPrep.Ready += ready
			l.photoPrep.Failed += failed
			l.photoPrep.Position = end
			l.touchPhotoPreparationLocked()
			err := l.savePhotoPreparationLocked()
			l.photoPrepMu.Unlock()
			if err != nil {
				return
			}
			pos = end
		}
		if ctx.Err() != nil {
			break
		}
		if generation != l.photoGeneration() {
			continue
		}
		// Reconcile against the cache: eviction during a pass is not readiness.
		ready := 0
		prepared := make(map[string]int)
		cacheEpoch := thumbnailCacheEpoch.Load()
		for _, f := range files {
			key, err := thumbnailKey(l.vault, f, 320)
			if err == nil && l.validatePreview(ctx, f) == nil {
				prepared[key]++
				if l.previewCached(key) {
					ready++
				}
			}
		}
		l.photoPrepMu.Lock()
		l.photoPrep.Ready = ready
		l.photoPrepared, l.photoCacheEpoch = prepared, cacheEpoch
		l.photoPrep.Retry = false
		l.photoPrepMu.Unlock()
		status := "complete"
		if ready < len(files) {
			status = "partial"
		}
		l.finishPhotoPreparation(status)
		return
	}
	l.finishPhotoPreparation("paused_locked")
}

func (l *Library) preparePhoto(ctx context.Context, f catalog.File, retry bool) error {
	key, err := thumbnailKey(l.vault, f, 320)
	if err != nil {
		return err
	}
	if l.previewCached(key) {
		if _, _, err := l.thumbnailFor(ctx, f, 320, true); err == nil {
			return l.clearPhotoFailure(key)
		}
	}
	if retry {
		if err := l.clearPhotoFailure(key); err != nil {
			return err
		}
	}
	if err := l.photoFailure(key); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, _, err = l.thumbnailFor(ctx, f, 320, true)
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrThumbnailUnavailable) || errors.Is(err, ErrPreviewTooLarge) || errors.Is(err, ErrPreviewCacheSkipped) {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if accessErr := l.validatePreview(ctx, f); accessErr != nil {
		return accessErr
	}
	if err == nil && !l.previewCached(key) {
		err = ErrPreviewCacheSkipped
	}
	if err != nil {
		if saveErr := l.savePhotoFailure(key, err); saveErr != nil {
			return saveErr
		}
	} else {
		_ = l.clearPhotoFailure(key)
	}
	return err
}
