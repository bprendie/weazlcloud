package library

import "context"

func (l *Library) Drain(ctx context.Context) error {
	for {
		l.batchMu.Lock()
		if !l.batchRunning {
			l.batchMu.Unlock()
			return nil
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
