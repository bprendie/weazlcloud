package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/photos"
)

type photoProgressContextKey struct{}

func withPhotoProgress(ctx context.Context, update func(int)) context.Context {
	return context.WithValue(ctx, photoProgressContextKey{}, update)
}

func reportPhotoProgress(ctx context.Context, value int) {
	if update, ok := ctx.Value(photoProgressContextKey{}).(func(int)); ok && update != nil {
		update(value)
	}
}

func (l *Library) setPhotoJobProgress(jobID, worker string, progress int) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if l.loadPhotoJobsLocked() != nil {
		return
	}
	for i := range l.photoJobs.Jobs {
		job := &l.photoJobs.Jobs[i]
		if job.ID == jobID && job.Status == photos.JobLeased && job.LeaseOwner == worker && progress > job.Progress && progress < 100 {
			job.Progress = progress
			return
		}
	}
}

func (l *Library) photoJobProgress() (int, int) {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if l.loadPhotoJobsLocked() != nil {
		return 0, 0
	}
	working, total := 0, 0
	for _, job := range l.photoJobs.Jobs {
		if job.Status == photos.JobLeased && job.Progress < 100 {
			working++
			total += job.Progress
		}
	}
	if working == 0 {
		return 0, 0
	}
	return working, total / working
}

func photoReadProgress(read, total int) int {
	if total <= 0 {
		return 10
	}
	return 10 + min(70, read*70/total)
}
