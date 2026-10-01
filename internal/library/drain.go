package library

import (
	"context"
	"time"
)

func (l *Library) Drain(ctx context.Context) error {
	l.stopPreviews()
	for {
		l.thumbMu.Lock()
		jobs := len(l.thumbJobs)
		l.thumbMu.Unlock()
		l.photoPrepMu.Lock()
		running := l.photoPrepRunning || l.photoResumeWaiting
		l.photoPrepMu.Unlock()
		l.metadataMu.Lock()
		metadataRunning := l.metadataRunning
		l.metadataMu.Unlock()
		if jobs == 0 && !running && !metadataRunning {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	for {
		l.batchMu.Lock()
		if !l.batchRunning {
			l.batchMu.Unlock()
			return l.backend.Drain(ctx)
		}
		done := l.batchDone
		l.batchMu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
