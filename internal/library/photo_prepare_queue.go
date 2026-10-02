package library

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func (l *Library) runPreparationQueue(parent context.Context, generation uint64, files map[string]catalog.File, retry bool) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	worker := fmt.Sprintf("%d-%d", time.Now().UnixNano(), generation)
	limit := max(1, previewPolicy.BackgroundWorkers)
	results := make(chan photoPreparationResult, limit)
	active := map[string]photos.MediaJob{}
	defer func() {
		cancel()
		var release []photoJobResult
		for len(active) > 0 {
			result := <-results
			delete(active, result.job.ID)
			release = append(release, photoJobResult{job: result.job, err: context.Canceled})
		}
		if len(release) > 0 {
			_ = l.settlePhotoJobs(release, worker)
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	checkpoint, renewed := time.Now(), time.Now()
	for ctx.Err() == nil {
		if l.photoStorageBusy() || generation != l.photoGeneration() {
			return nil
		}
		jobs, err := l.leasePhotoJobs(worker, limit-len(active))
		if err != nil {
			return err
		}
		for _, job := range jobs {
			active[job.ID] = job
			file, ok := files[job.AssetID]
			if !ok || file.Revision != job.Revision {
				results <- photoPreparationResult{job: job, err: ErrThumbnailUnavailable}
				continue
			}
			go func(job photos.MediaJob, file catalog.File) {
				l.setPhotoJobProgress(job.ID, worker, 5)
				work := withPhotoProgress(ctx, func(progress int) { l.setPhotoJobProgress(job.ID, worker, progress) })
				results <- photoPreparationResult{job: job, err: l.preparePhoto(work, file, retry)}
			}(job, file)
		}
		if len(active) == 0 {
			pending, leased, _, _, err := l.photoJobCounts()
			if err != nil {
				return err
			}
			if pending == 0 && leased == 0 {
				return l.checkpointPreparationProgress()
			}
		}
		var settled []photoJobResult
		var fatal error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-results:
			delete(active, result.job.ID)
			settled = append(settled, photoJobResult{job: result.job, err: result.err})
			if errors.Is(result.err, errPhotoFailureRecord) {
				fatal = result.err
			}
			// Coalesce results already available; never wait for a slow neighbor.
		drain:
			for len(settled) < 100 {
				select {
				case result := <-results:
					delete(active, result.job.ID)
					settled = append(settled, photoJobResult{job: result.job, err: result.err})
					if errors.Is(result.err, errPhotoFailureRecord) {
						fatal = result.err
					}
				default:
					break drain
				}
			}
		case <-ticker.C:
		}
		if len(settled) > 0 {
			if err := l.settlePhotoJobs(settled, worker); err != nil {
				return err
			}
		}
		if fatal != nil {
			return fatal
		}
		if time.Since(renewed) >= 30*time.Second {
			if err := l.renewPhotoJobs(worker, active); err != nil {
				return err
			}
			renewed = time.Now()
		}
		if time.Since(checkpoint) >= 250*time.Millisecond {
			if err := l.checkpointPreparationProgress(); err != nil {
				return err
			}
			checkpoint = time.Now()
		}
	}
	return ctx.Err()
}

func (l *Library) checkpointPreparationProgress() error {
	_, _, ready, failed, err := l.photoJobCounts()
	if err != nil {
		return err
	}
	l.photoPrepMu.Lock()
	defer l.photoPrepMu.Unlock()
	l.photoPrep.Ready, l.photoPrep.BundleReady, l.photoPrep.Failed, l.photoPrep.Position = ready, ready, failed, ready+failed
	l.touchPhotoPreparationLocked()
	return l.savePhotoPreparationLocked()
}

func (l *Library) renewPhotoJobs(worker string, active map[string]photos.MediaJob) error {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return err
	}
	until := time.Now().Add(photoJobLeaseTime)
	for id := range active {
		l.photoJobs.Renew(id, worker, until)
	}
	return l.savePhotoJobsLocked()
}
