package library

import (
	"context"
	"sort"
	"sync"
)

// Component gates are independent of catalog locking. A caller owns its reader
// until storage has stopped, including after cancellation.
type photoComponentWork struct {
	batchCancel context.CancelFunc
	mu          sync.Mutex
	gates       map[string]chan struct{}
	slots       chan struct{}
	active      map[*int]context.CancelFunc
	changed     chan struct{}
	blocked     context.Context
	running     bool
	queue       chan photoStreamRequest
}

func (l *Library) componentWork() *photoComponentWork {
	l.componentOnce.Do(func() {
		l.components = &photoComponentWork{gates: make(map[string]chan struct{}), slots: make(chan struct{}, 8), active: make(map[*int]context.CancelFunc), changed: make(chan struct{}), queue: make(chan photoStreamRequest, 8)}
	})
	return l.components
}

func (l *Library) beginPhotoComponent(parent context.Context) (context.Context, func(), error) {
	ctx, release := l.previewContext(parent)
	ctx, cancel := context.WithCancel(ctx)
	w := l.componentWork()
	select {
	case w.slots <- struct{}{}:
	case <-ctx.Done():
		cancel()
		release()
		return nil, nil, ctx.Err()
	}
	w.mu.Lock()
	if w.blocked == l.vault.Session() || ctx.Err() != nil {
		w.mu.Unlock()
		<-w.slots
		cancel()
		release()
		return nil, nil, context.Canceled
	}
	id := new(int)
	w.active[id] = cancel
	w.mu.Unlock()
	activity := l.trackStorage()
	return ctx, func() {
		cancel()
		release()
		activity()
		w.mu.Lock()
		delete(w.active, id)
		close(w.changed)
		w.changed = make(chan struct{})
		w.mu.Unlock()
		<-w.slots
	}, nil
}

func (l *Library) gatePhotoComponents(ctx context.Context, names ...string) (func(), error) {
	w := l.componentWork()
	sort.Strings(names)
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for i, name := range names {
		if i > 0 && name == names[i-1] {
			continue
		}
		for {
			w.mu.Lock()
			busy := w.gates[name]
			if busy == nil {
				done := make(chan struct{})
				w.gates[name] = done
				releases = append(releases, func() { w.mu.Lock(); delete(w.gates, name); close(done); w.mu.Unlock() })
				w.mu.Unlock()
				break
			}
			w.mu.Unlock()
			select {
			case <-busy:
			case <-ctx.Done():
				release()
				return nil, ctx.Err()
			}
		}
	}
	return release, nil
}

func (l *Library) drainPhotoComponents(ctx context.Context, locking bool) error {
	w := l.componentWork()
	w.mu.Lock()
	if locking {
		w.blocked = l.vault.Session()
	}
	if w.batchCancel != nil {
		w.batchCancel()
	}
	for _, cancel := range w.active {
		cancel()
	}
	for len(w.active) != 0 {
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
		w.mu.Lock()
	}
	w.mu.Unlock()
	return nil
}

// Direct Resource.LockVault has no registry drain after this call. Keep the
// key available until every source reader and backend process has relinquished
// ownership; an arbitrary io.Reader cannot be forcibly interrupted by Go.
func (l *Library) stopPhotoComponentsForLock() {
	_ = l.drainPhotoComponents(context.Background(), true)
}
