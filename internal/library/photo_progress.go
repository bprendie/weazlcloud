package library

import (
	"context"
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
	l.photoJobs.SetProgress(jobID, worker, progress)
}

func (l *Library) photoJobProgress() (int, int) {
	// Queue initialization and cache inventory belong to the worker. A status
	// poll must not wait behind either, or hold up a timeline's resume check.
	if !l.photoJobsMu.TryLock() {
		return 0, 0
	}
	defer l.photoJobsMu.Unlock()
	if !l.photoJobsLoaded {
		return 0, 0
	}
	return l.photoJobs.Progress()
}

func photoReadProgress(read, total int) int {
	if total <= 0 {
		return 10
	}
	return 10 + min(70, read*70/total)
}
