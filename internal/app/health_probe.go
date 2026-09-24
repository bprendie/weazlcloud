package app

import (
	"context"
	"sync"
	"time"
)

// A blocked filesystem sync cannot be interrupted by an HTTP cancellation.
// Keep at most one durable probe in flight instead of launching another sync
// every time a health-check client times out during a large disk transfer.
type storageProbe struct {
	mu       sync.Mutex
	check    func() error
	done     chan struct{}
	err      error
	finished time.Time
}

func (p *storageProbe) ready(ctx context.Context) error {
	p.mu.Lock()
	if !p.finished.IsZero() && time.Since(p.finished) < 5*time.Second {
		err := p.err
		p.mu.Unlock()
		return err
	}
	if p.done == nil {
		p.done = make(chan struct{})
		go p.run(p.done)
	}
	done := p.done
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.err
	}
}

func (p *storageProbe) run(done chan struct{}) {
	err := p.check()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err, p.finished = err, time.Now()
	close(done)
	p.done = nil
}
