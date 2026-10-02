package library

import (
	"context"
	"errors"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

type photoPreparationResult struct {
	job photos.MediaJob
	err error
}

// Each pass has a stable snapshot. A restart scans identities again, using
// authenticated cache entries and durable failures instead of slice positions.
func (l *Library) runPhotoPreparationParallel(parent context.Context) {
	ctx := parent
	defer func() {
		l.photoPrepMu.Lock()
		l.photoPrepRunning = false
		l.photoPrepCancel = nil
		stoppingForLock := l.photoStoppingForLock
		l.photoStoppingForLock = false
		restart := !stoppingForLock && !l.photoPrep.Paused && (l.photoPrep.Status == "queued" || (l.photoPrep.Status == "paused_locked" && l.vault.Unlocked()))
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
			if photoPreviewable(f.Path) {
				files = append(files, f)
			}
		}
		l.photoMu.Unlock()
		l.photoPrepMu.Lock()
		if l.photoPrep.Paused {
			l.photoPrepMu.Unlock()
			return
		}
		retry := l.photoPrep.Retry
		autoOnly := l.photoPrep.AutoOnly
		l.photoPrep.Generation, l.photoPrep.Total = generation, len(files)
		l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
		l.photoPrepMu.Unlock()
		if previewPolicy.Schedule == "quiet" {
			l.finishPhotoPreparation("paused_schedule")
			return
		}
		fileByID := make(map[string]catalog.File, len(files))
		jobFiles := make([]photoJobFile, 0, len(files))
		for _, file := range files {
			fileByID[file.EntryID] = file
			cacheKey, _ := thumbnailKey(l.vault, file, 320)
			viewerKey, _ := thumbnailKey(l.vault, file, 1280)
			jobFiles = append(jobFiles, photoJobFile{id: file.EntryID, revision: file.Revision, cacheKey: cacheKey, viewerKey: viewerKey})
		}
		if autoOnly {
			assetIDs, err := l.photoJobAssetIDs(true)
			if err != nil {
				l.setPhotoPreparationError(err)
				return
			}
			filtered := make([]catalog.File, 0, len(assetIDs))
			filteredJobs := make([]photoJobFile, 0, len(assetIDs))
			for _, file := range files {
				if _, ok := assetIDs[file.EntryID]; ok {
					filtered = append(filtered, file)
					cacheKey, _ := thumbnailKey(l.vault, file, 320)
					viewerKey, _ := thumbnailKey(l.vault, file, 1280)
					filteredJobs = append(filteredJobs, photoJobFile{id: file.EntryID, revision: file.Revision, cacheKey: cacheKey, viewerKey: viewerKey})
				}
			}
			files, jobFiles, fileByID = filtered, filteredJobs, make(map[string]catalog.File, len(filtered))
			for _, file := range files {
				fileByID[file.EntryID] = file
			}
			l.photoPrepMu.Lock()
			l.photoPrep.Total = len(files)
			l.photoPrepMu.Unlock()
		}
		if _, err := l.syncPhotoJobs(jobFiles, retry); err != nil {
			l.setPhotoPreparationError(err)
			return
		}
		if err := l.runPreparationQueue(ctx, generation, fileByID, retry); err != nil && ctx.Err() == nil {
			l.setPhotoPreparationError(err)
			return
		}
		if l.photoStorageBusy() && ctx.Err() == nil {
			l.finishPhotoPreparation("paused_storage")
			l.waitForPhotoStorage()
			return
		}
		if ctx.Err() != nil {
			break
		}
		if generation != l.photoGeneration() {
			continue
		}
		// Reconcile against the cache: eviction during a pass is not readiness.
		ready, bundles := 0, 0
		prepared := make(map[string]int)
		preparedBundles := make(map[string]string)
		cacheEpoch := thumbnailCacheEpoch.Load()
		for _, f := range files {
			key, err := thumbnailKey(l.vault, f, 320)
			if err == nil && l.validatePreview(ctx, f) == nil {
				prepared[key]++
				if l.validCachedPreview(key) {
					ready++
					viewer, _ := thumbnailKey(l.vault, f, 1280)
					preparedBundles[key] = viewer
					if l.validCachedPreview(viewer) {
						bundles++
					}
				}
			}
		}
		l.photoPrepMu.Lock()
		l.photoPrep.Ready, l.photoPrep.BundleReady = ready, bundles
		l.photoPrepared, l.photoPreparedBundles, l.photoCacheEpoch = prepared, preparedBundles, cacheEpoch
		l.photoPrep.Retry = false
		l.photoPrepMu.Unlock()
		_, _, succeeded, failed, _ := l.photoJobCounts()
		l.photoPrepMu.Lock()
		l.photoPrep.Position = succeeded + failed
		l.photoPrep.Failed = failed
		l.photoPrepMu.Unlock()
		status := "complete"
		if ready < len(files) || bundles < len(files) || failed > 0 {
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
	if l.validCachedPreview(key) {
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
	if err == nil && !l.validCachedPreview(key) {
		err = ErrPreviewCacheSkipped
	}
	if err != nil {
		if !photoJobRetryable(err) {
			if saveErr := l.savePhotoFailure(key, err); saveErr != nil {
				return saveErr
			}
		}
	} else {
		_ = l.clearPhotoFailure(key)
	}
	return err
}

func (l *Library) validCachedPreview(key string) bool {
	_, _, ok := l.readThumbnailCache(key)
	return ok
}

func (l *Library) setPhotoPreparationError(err error) {
	l.photoPrepMu.Lock()
	l.photoPrep.Status = "paused_error"
	l.photoPrep.Error = err.Error()
	l.touchPhotoPreparationLocked()
	l.savePhotoPreparationLocked()
	l.photoPrepMu.Unlock()
}
