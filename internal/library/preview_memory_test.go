package library

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPreviewMemoryCancelledWaitDoesNotLeak(t *testing.T) {
	b := newPreviewMemoryBudget(10)
	release, err := b.acquire(context.Background(), 6)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := b.acquire(ctx, 6); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	b.mu.Lock()
	waiters := b.foregroundWaiters
	b.mu.Unlock()
	if waiters != 0 {
		t.Fatalf("cancelled foreground waiter leaked priority: %d", waiters)
	}
	release()
	release() // idempotent release must not over-credit the budget
	all, err := b.acquire(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	all()
	background, err := b.acquireBackground(context.Background(), 10)
	if err != nil {
		t.Fatalf("cancelled waiter blocked background reservation: %v", err)
	}
	background()
	if _, err := b.acquire(context.Background(), 11); !errors.Is(err, ErrPreviewTooLarge) {
		t.Fatal(err)
	}
}

func TestPreviewMemoryConcurrentReservationsProgress(t *testing.T) {
	b := newPreviewMemoryBudget(10)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := b.acquire(ctx, 6)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			time.Sleep(time.Millisecond)
		}()
	}
	wg.Wait()
	if b.used != 0 {
		t.Fatalf("leaked %d bytes", b.used)
	}
}

func TestPreviewMemoryForegroundGetsNextReleasedCapacity(t *testing.T) {
	b := newPreviewMemoryBudget(10)
	hold, err := b.acquireBackground(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	foregroundReady := make(chan struct{})
	foregroundRelease := make(chan struct{})
	backgroundAcquired := make(chan struct{})
	go func() {
		release, acquireErr := b.acquire(context.Background(), 6)
		if acquireErr != nil {
			return
		}
		close(foregroundReady)
		<-foregroundRelease
		release()
	}()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		waiting := b.foregroundWaiters == 1
		b.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground reservation did not register")
		}
		time.Sleep(time.Millisecond)
	}
	go func() {
		release, acquireErr := b.acquireBackground(context.Background(), 6)
		if acquireErr != nil {
			return
		}
		close(backgroundAcquired)
		release()
	}()
	hold()
	select {
	case <-foregroundReady:
	case <-time.After(time.Second):
		t.Fatal("foreground reservation did not acquire released capacity")
	}
	select {
	case <-backgroundAcquired:
		t.Fatal("background work acquired memory ahead of foreground")
	default:
	}
	close(foregroundRelease)
	select {
	case <-backgroundAcquired:
	case <-time.After(time.Second):
		t.Fatal("background work did not resume after foreground released memory")
	}
}
