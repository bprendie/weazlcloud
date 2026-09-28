package library

import (
	"context"
	"errors"
)

var previewQueued = make(chan struct{}, 256)
var errPreviewBusy = errors.New("preview queue is full; retry shortly")

// A request owns a waiter, not the renderer. Navigation cancels rendering only
// after the final waiter leaves; owner revocation cancels every worker lease.
func (l *Library) coalescedPreview(ctx context.Context, key string, render func(context.Context) ([]byte, string, error)) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	l.thumbMu.Lock()
	job := l.thumbJobs[key]
	if job != nil && job.waiters == 0 {
		l.thumbMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-job.done:
		}
		return l.coalescedPreview(ctx, key, render)
	}
	if job == nil {
		select {
		case previewQueued <- struct{}{}:
		default:
			l.thumbMu.Unlock()
			return nil, "", errPreviewBusy
		}
		work, release := l.previewContext(context.Background())
		work, cancel := context.WithCancel(work)
		job = &thumbnailJob{done: make(chan struct{}), cancel: cancel}
		l.thumbJobs[key] = job
		go func() {
			defer func() { <-previewQueued }()
			defer release()
			defer cancel()
			job.body, job.contentType, job.err = render(work)
			l.finishThumbnailJob(key, job)
		}()
	}
	job.waiters++
	l.thumbMu.Unlock()
	defer func() {
		l.thumbMu.Lock()
		job.waiters--
		if job.waiters == 0 && job.cancel != nil {
			job.cancel()
		}
		l.thumbMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-job.done:
		return job.body, job.contentType, job.err
	}
}

func (l *Library) finishThumbnailJob(key string, job *thumbnailJob) {
	l.thumbMu.Lock()
	delete(l.thumbJobs, key)
	close(job.done)
	l.thumbMu.Unlock()
}
