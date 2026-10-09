package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/restic/restic/internal/repository"
)

var errIndexStale = errors.New("source missing from resident index")

// LoadIndex replaces the repository's index. Never race it with authenticated
// reads. Misses share one refresh, capped at one per second during continuous
// ingest; the repository key, process and resource admission remain resident.
type indexedRepository struct {
	repo        *repository.Repository
	mu          sync.RWMutex
	generation  uint64
	nextRefresh time.Time
}

func (r *indexedRepository) read(ctx context.Context, fn func() error) error {
	r.mu.RLock()
	generation := r.generation
	err := fn()
	r.mu.RUnlock()
	if !errors.Is(err, errIndexStale) || ctx.Err() != nil {
		return err
	}
	if err = r.refresh(ctx, generation); err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Only one retry. A corrupt/missing source retains the parent's bounded
	// fallback instead of causing an unbounded index reload loop.
	return fn()
}

func (r *indexedRepository) refresh(ctx context.Context, generation uint64) error {
	for {
		r.mu.Lock()
		if err := ctx.Err(); err != nil {
			r.mu.Unlock()
			return err
		}
		if generation != r.generation {
			r.mu.Unlock()
			return nil
		}
		wait := time.Until(r.nextRefresh)
		if wait <= 0 {
			err := r.repo.LoadIndex(ctx, nil)
			r.generation++
			r.nextRefresh = time.Now().Add(time.Second)
			r.mu.Unlock()
			return err
		}
		r.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
