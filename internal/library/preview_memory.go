package library

import (
	"context"
	"sync"
)

// Reservations are all-or-nothing. Partial channel acquisitions can deadlock
// competing decoders or leak capacity when a waiting request is cancelled.
type previewMemoryBudget struct {
	mu                sync.Mutex
	limit, used       int64
	foregroundWaiters int
	changed           chan struct{}
}

func newPreviewMemoryBudget(limit int64) *previewMemoryBudget {
	return &previewMemoryBudget{limit: limit, changed: make(chan struct{})}
}

// Resident caches/readers must not wait while retaining a partial render budget.
func (b *previewMemoryBudget) tryAcquire(size int64) (func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size <= 0 || size > b.limit-b.used || b.foregroundWaiters > 0 {
		return nil, false
	}
	b.used += size
	var once sync.Once
	return func() { once.Do(func() { b.mu.Lock(); b.used -= size; b.notifyLocked(); b.mu.Unlock() }) }, true
}

func (b *previewMemoryBudget) acquire(ctx context.Context, size int64) (func(), error) {
	return b.acquireWithPriority(ctx, size, false)
}

func (b *previewMemoryBudget) acquireBackground(ctx context.Context, size int64) (func(), error) {
	return b.acquireWithPriority(ctx, size, true)
}

func (b *previewMemoryBudget) acquireWithPriority(ctx context.Context, size int64, background bool) (func(), error) {
	if size <= 0 || size > b.limit {
		return nil, ErrPreviewTooLarge
	}
	previewRAM.reclaim(b, size)
	registered := false
	for {
		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			if registered {
				b.foregroundWaiters--
				b.notifyLocked()
			}
			b.mu.Unlock()
			return nil, err
		}
		if !background && size > b.limit-b.used && !registered {
			b.foregroundWaiters++
			registered = true
			b.notifyLocked()
		}
		if (!background || b.foregroundWaiters == 0) && size <= b.limit-b.used {
			if registered {
				b.foregroundWaiters--
				registered = false
				b.notifyLocked()
			}
			b.used += size
			b.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					b.mu.Lock()
					b.used -= size
					b.notifyLocked()
					b.mu.Unlock()
				})
			}, nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			if registered {
				b.mu.Lock()
				b.foregroundWaiters--
				b.notifyLocked()
				b.mu.Unlock()
			}
			return nil, ctx.Err()
		}
	}
}

func (b *previewMemoryBudget) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}
