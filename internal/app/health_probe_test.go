package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadinessCoalescesBlockedDurableProbes(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	p := &storageProbe{check: func() error { calls.Add(1); close(started); <-release; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.ready(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("want cancellation, got %v", err)
			}
		}()
	}
	<-started
	cancel()
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("launched %d filesystem probes", calls.Load())
	}
	close(release)
	if err := p.ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("fresh durable result was not cached")
	}
}

func TestReadinessCachesFailuresAndRechecksAfterExpiry(t *testing.T) {
	want := errors.New("volume unavailable")
	var calls atomic.Int32
	p := &storageProbe{check: func() error { calls.Add(1); return want }}
	for i := 0; i < 2; i++ {
		if err := p.ready(context.Background()); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("failure was not cached")
	}
	p.mu.Lock()
	p.finished = time.Now().Add(-6 * time.Second)
	p.mu.Unlock()
	if err := p.ready(context.Background()); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired result was reused")
	}
}
