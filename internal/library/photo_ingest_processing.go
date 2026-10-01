package library

import (
	"context"
	"log"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func (l *Library) ackPhotoIngest(files []catalog.File) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	versions := make(map[string]uint64, len(files))
	for _, file := range files {
		versions[file.EntryID] = file.Revision
	}
	return l.catalog.AcknowledgePhotoProcessing(versions)
}

func (l *Library) retryPhotoIngest(files []catalog.File) {
	ctx, release := l.previewContext(context.Background())
	defer release()
	for ctx.Err() == nil {
		if err := l.queuePhotoIngestFiles(files); err == nil {
			return
		} else {
			log.Printf("photo ingest processing checkpoint will retry: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (l *Library) PhotoProcessingState(ctx context.Context, id string) (string, error) {
	item, err := l.PhotoDetail(ctx, id)
	if err != nil {
		item, err = l.PhotoDetail(ctx, id, true)
		if err != nil {
			return "pending", err
		}
	}
	// Embedded-metadata extraction can advance the catalog revision after the
	// render job was created. Cache identity follows pixels, not that revision.
	l.photoMu.Lock()
	file, found := l.photoByID[id]
	l.photoMu.Unlock()
	if found {
		key, keyErr := thumbnailKey(l.vault, file, 320)
		if keyErr == nil {
			body, _, ready := l.readThumbnailCache(key)
			clear(body)
			if ready {
				return "ready", nil
			}
		}
	}

	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return "pending", err
	}
	for _, job := range l.photoJobs.Jobs {
		if job.AssetID != id || job.Revision != item.Revision {
			continue
		}
		switch job.Status {
		case photos.JobSucceeded:
			return "ready", nil
		case photos.JobFailed:
			return "failed", nil
		case photos.JobPending, photos.JobLeased:
			return "processing", nil
		}
	}
	return "pending", nil
}
