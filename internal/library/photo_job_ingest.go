package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"time"
)

func (l *Library) queuePhotoIngestFiles(files []catalog.File) error {
	if len(files) == 0 || !l.vault.Unlocked() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	l.mu.Lock()
	err := l.ensurePhotoIndexLocked(ctx)
	if err == nil {
		l.photoMu.Lock()
		current := make([]catalog.File, 0, len(files))
		for _, file := range files {
			if found, ok := l.photoByID[file.EntryID]; ok && found.PhotoParentID == "" {
				current = append(current, found)
			}
		}
		files = current
		l.photoMu.Unlock()
	}
	l.mu.Unlock()
	cancel()
	if err != nil {
		return err
	}
	l.photoJobsMu.Lock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		l.photoJobsMu.Unlock()
		return err
	}
	queued := false
	for _, file := range files {
		if photoOriginalPreviewUnsupported(file) || file.Folder || file.PhotoParentID != "" || !photoPreviewable(file.Path) {
			continue
		}
		job := photos.NewMediaJob(l.ownerID, file.EntryID, file.Revision, photoJobOperation, thumbnailRenderer, 1)
		l.photoJobs.Upsert(job)
		if current, ok := l.photoJobs.Get(job.ID); ok && current.Status == photos.JobPending {
			queued = true
		}
	}
	if err := l.savePhotoJobsLocked(); err != nil {
		l.photoJobsMu.Unlock()
		return err
	}
	l.photoJobsMu.Unlock()
	if err := l.queueMetadataIngest(files); err != nil {
		return err
	}
	if err := l.ackPhotoIngest(files); err != nil {
		return err
	}
	if !queued {
		return nil
	}

	l.photoPrepMu.Lock()
	l.loadPhotoPreparationLocked()
	if !l.photoPrep.Paused {
		if !l.photoPrep.Enabled {
			l.photoPrep.AutoOnly = true
		}
		l.photoPrep.Enabled = true
		l.photoPrep.Status = "queued"
		l.photoPrep.Error = ""
		if l.photoPrep.AutoOnly {
			l.photoPrep.Retry = false
		}
		if l.photoPrep.AutoOnly {
			l.photoPrep.Total = len(files)
		}
		l.photoPrep.Generation = l.photoGeneration()
		l.touchPhotoPreparationLocked()
		l.savePhotoPreparationLocked()
	}
	start := !l.photoPrep.Paused
	l.photoPrepMu.Unlock()
	if start {
		l.startPhotoPreparationWorker()
	}
	return nil
}

func photoJobErrorCategory(err error) string {
	switch {
	case errors.Is(err, previewrpc.ErrUnavailable):
		return "worker_unavailable"
	case errors.Is(err, previewrpc.ErrTooLarge):
		return "unsupported_size"
	case errors.Is(err, previewrpc.ErrRejected):
		return "invalid_or_unsupported_media"
	case errors.Is(err, ErrPreviewTooLarge):
		return "unsupported_size"
	case errors.Is(err, ErrThumbnailUnavailable):
		return "invalid_or_unsupported_media"
	case errors.Is(err, ErrPreviewCacheSkipped):
		return "cache_capacity"
	case errors.Is(err, ErrPhotoPreviouslyFailed):
		return "previous_terminal_failure"
	default:
		return "source_or_worker_error"
	}
}

func photoJobRetryable(err error) bool {
	return !errors.Is(err, previewrpc.ErrTooLarge) && !errors.Is(err, previewrpc.ErrRejected) && !errors.Is(err, ErrPreviewTooLarge) && !errors.Is(err, ErrThumbnailUnavailable) && !errors.Is(err, ErrPreviewCacheSkipped) && !errors.Is(err, ErrPhotoPreviouslyFailed)
}
