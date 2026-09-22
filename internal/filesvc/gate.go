package filesvc

import (
	"context"
	"sync"
)

type userGate struct {
	blocked bool
	active  int
	zero    chan struct{}
	cancels map[uint64]context.CancelFunc
	next    uint64
}

func newUserGate() *userGate {
	z := make(chan struct{})
	close(z)
	return &userGate{zero: z, cancels: make(map[uint64]context.CancelFunc)}
}

// Enter leases one user-scoped request and makes it cancellable by Block.
func (r *Registry) Enter(ctx context.Context, id string) (context.Context, func(), bool) {
	r.mu.Lock()
	g := r.gateLocked(id)
	if g.blocked {
		r.mu.Unlock()
		return ctx, func() {}, false
	}
	c, cancel := context.WithCancel(ctx)
	if g.active == 0 {
		g.zero = make(chan struct{})
	}
	g.active++
	g.next++
	key := g.next
	g.cancels[key] = cancel
	r.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			r.mu.Lock()
			delete(g.cancels, key)
			g.active--
			if g.active == 0 {
				close(g.zero)
			}
			r.mu.Unlock()
		})
	}
	return c, release, true
}

// Block closes the admission gate, cancels active requests, then waits for all
// handlers to release their leases before owner data can be removed.
func (r *Registry) Block(ctx context.Context, id string) error {
	r.mu.Lock()
	g := r.gateLocked(id)
	g.blocked = true
	for _, cancel := range g.cancels {
		cancel()
	}
	zero := g.zero
	r.mu.Unlock()
	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Registry) Open(id string) { r.mu.Lock(); r.gateLocked(id).blocked = false; r.mu.Unlock() }

func (r *Registry) gateLocked(id string) *userGate {
	g := r.gates[id]
	if g == nil {
		g = newUserGate()
		r.gates[id] = g
	}
	return g
}

func (r *Registry) DrainResource(ctx context.Context, id string) error {
	r.mu.Lock()
	resource := r.items[id]
	r.mu.Unlock()
	if resource == nil {
		return nil
	}
	if err := resource.Archives.Drain(ctx); err != nil {
		return err
	}
	if err := resource.Lib.Drain(ctx); err != nil {
		return err
	}
	resource.Vault.Lock()
	resource.Changes.Close()
	r.mu.Lock()
	delete(r.items, id)
	r.mu.Unlock()
	return nil
}
