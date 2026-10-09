package library

import (
	"context"
	"time"
)

func (l *Library) Drain(ctx context.Context) error {
	l.stopPreviews()
	l.stopPreviewReader()
	for {
		l.thumbMu.Lock()
		jobs := len(l.thumbJobs) + len(l.bundleJobs)
		l.thumbMu.Unlock()
		l.photoPrepMu.Lock()
		running := l.photoPrepRunning || l.photoResumeWaiting || l.photoCacheChecking
		l.photoPrepMu.Unlock()
		l.metadataMu.Lock()
		metadataRunning := l.metadataRunning
		l.metadataMu.Unlock()
		l.readerMu.Lock()
		readerRunning := l.previewReader != nil
		l.readerMu.Unlock()
		l.liveMu.Lock()
		liveRunning := l.liveRunning
		l.liveMu.Unlock()
		if jobs == 0 && !running && !metadataRunning && !readerRunning && !liveRunning {
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
			if err := l.exportPhotoJobs(); err != nil {
				return err
			}
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
