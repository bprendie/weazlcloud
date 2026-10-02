package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/photos"
	"time"
)

const (
	photoJobStoreVersion = 1
	photoJobStoreLimit   = 64 << 20
	photoJobCountLimit   = 200_000
	photoJobOperation    = "thumbnail:320"
	photoJobLeaseTime    = 2 * time.Minute
)

func validPhotoJob(job photos.MediaJob) bool {
	return len(job.ID) == 32 && len(job.OwnerID) <= 128 && len(job.AssetID) <= 128 && job.Revision > 0 && job.Operation == photoJobOperation && len(job.Renderer) > 0 && len(job.Renderer) <= 128 && job.Attempts >= 0 && job.Attempts <= photos.MaxJobAttempts && job.Progress >= 0 && job.Progress <= 100 && (job.Status == photos.JobPending || job.Status == photos.JobLeased || job.Status == photos.JobSucceeded || job.Status == photos.JobFailed || job.Status == photos.JobCanceled)
}

func (l *Library) syncPhotoJobs(files []photoJobFile, retry bool) (int, error) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return 0, err
	}
	current := make(map[string]struct{}, len(files))
	for _, file := range files {
		job := photos.NewMediaJob(l.ownerID, file.id, file.revision, photoJobOperation, thumbnailRenderer, 3)
		current[job.ID] = struct{}{}
		l.photoJobs.Upsert(job)
		_, _, cacheValid := l.readThumbnailCache(file.cacheKey)
		if cacheValid && file.viewerKey != "" {
			_, _, cacheValid = l.readThumbnailCache(file.viewerKey)
		}
		stored, _ := l.photoJobs.Get(job.ID)
		if cacheValid {
			stored.Status, stored.Progress = photos.JobSucceeded, 100
			stored.LeaseOwner, stored.LeaseUntil = "", time.Time{}
			stored.ErrorCategory = ""
		} else if stored.Status == photos.JobSucceeded {
			stored.Status, stored.Progress = photos.JobPending, 0
			stored.NextAttemptAt = time.Time{}
		}
		l.photoJobs.Replace(stored)
	}
	kept := l.photoJobs.Jobs[:0]
	for _, job := range l.photoJobs.Jobs {
		if _, ok := current[job.ID]; ok {
			if retry && job.Status == photos.JobFailed {
				job.Status, job.Attempts, job.Progress, job.ErrorCategory = photos.JobPending, 0, 0, ""
				job.NextAttemptAt = time.Time{}
			}
			kept = append(kept, job)
		}
	}
	l.photoJobs.Jobs = kept
	l.photoJobs.Reindex()
	l.photoJobsSnapshot = true
	if err := l.savePhotoJobsLocked(); err != nil {
		return 0, err
	}
	return len(current), nil
}

type photoJobFile struct {
	id        string
	revision  uint64
	cacheKey  string
	viewerKey string
}

type photoJobResult struct {
	job photos.MediaJob
	err error
}

func (l *Library) leasePhotoJobs(worker string, limit int) ([]photos.MediaJob, error) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return nil, err
	}
	jobs := l.photoJobs.Lease(worker, time.Now().UTC(), photoJobLeaseTime, limit)
	if len(jobs) > 0 {
		if err := l.savePhotoJobsLocked(); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func (l *Library) settlePhotoJobs(results []photoJobResult, worker string) error {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, result := range results {
		switch {
		case result.err == nil:
			l.photoJobs.Complete(result.job.ID, worker)
		case errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded):
			l.photoJobs.Release(result.job.ID, worker)
		default:
			l.photoJobs.Fail(result.job.ID, worker, photoJobErrorCategory(result.err), photoJobRetryable(result.err), now)
		}
	}
	return l.savePhotoJobsLocked()
}

func (l *Library) photoJobCounts() (int, int, int, int, error) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return 0, 0, 0, 0, err
	}
	pending, leased, succeeded, failed := l.photoJobs.Counts()
	return pending, leased, succeeded, failed, nil
}

func (l *Library) photoJobAssetIDs(autoOnly bool) (map[string]struct{}, error) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return nil, err
	}
	ids := make(map[string]struct{})
	for _, job := range l.photoJobs.Jobs {
		if autoOnly && job.Priority > 1 {
			continue
		}
		if job.Status == photos.JobPending || job.Status == photos.JobLeased || job.Status == photos.JobFailed {
			ids[job.AssetID] = struct{}{}
		}
	}
	return ids, nil
}
