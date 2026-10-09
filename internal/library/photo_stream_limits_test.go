package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPhotoStreamAdmissionIsBounded(t *testing.T) {
	l, b := componentFixture(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b.before = func(context.Context, []StreamInput) error { once.Do(func() { close(entered); <-unblock }); return nil }
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		go func(i int) {
			_, e := storeComponent(l, context.Background(), fmt.Sprint(i), []byte("bounded"))
			errs <- e
		}(i)
	}
	<-entered
	w := l.componentWork()
	w.mu.Lock()
	active := len(w.active)
	pending := len(w.queue)
	w.mu.Unlock()
	if active > 8 || pending > 8 {
		t.Fatalf("active=%d pending=%d", active, pending)
	}
	close(unblock)
	for i := 0; i < 24; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range b.counts {
		if n > 8 {
			t.Fatalf("batch size %d", n)
		}
	}
}

func TestPhotoStreamsLargeAndByteCapUseStdin(t *testing.T) {
	for _, size := range []int64{40 << 20, 1 << 40} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			l, b := componentFixture(t)
			errs := make(chan error, 2)
			// Deliberately short input proves declared sizes never allocate payload RAM.
			for i := 0; i < 2; i++ {
				go func(i int) {
					_, e := l.StorePhotoComponent(context.Background(), fmt.Sprintf(".weazl-mobile-pending/%d", i), bytes.NewReader([]byte("tiny")), size, componentHash([]byte("tiny")))
					errs <- e
				}(i)
			}
			for i := 0; i < 2; i++ {
				if err := awaitComponent(t, errs); !errors.Is(err, ErrPhotoComponentChecksum) {
					t.Fatal(err)
				}
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.batches != 0 || b.singles != 2 {
				t.Fatalf("batch=%d singles=%d", b.batches, b.singles)
			}
		})
	}
}

func TestPhotoStreamsMissingHelperFallback(t *testing.T) {
	l, b := componentFixture(t)
	b.before = func(context.Context, []StreamInput) error { return ErrPhotoStreamsUnavailable }
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
	}
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	if b.singles != 2 {
		t.Fatalf("single writes=%d", b.singles)
	}
}

func TestPhotoStreamsSameNameGateAndCanceledWaiter(t *testing.T) {
	l, b := componentFixture(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	b.before = func(context.Context, []StreamInput) error { close(entered); <-unblock; return nil }
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := l.StorePhotoComponent(ctx, ".weazl-mobile-pending/one", failPhotoComponentReader{t}, 3, componentHash([]byte("one")))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	retry := make(chan error, 1)
	go func() {
		_, e := l.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/one", failPhotoComponentReader{t}, 3, componentHash([]byte("one")))
		retry <- e
	}()
	close(unblock)
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitComponent(t, retry); err != nil {
		t.Fatal(err)
	}
	if b.batches != 1 || b.singles != 0 {
		t.Fatal("same-name retry rewrote bytes")
	}
}

func TestPhotoStreamsRetryIsolationBypassesHelper(t *testing.T) {
	l, b := componentFixture(t)
	b.before = func(context.Context, []StreamInput) error {
		t.Error("retry used batch helper")
		return errors.New("bad peer")
	}
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			_, e := storeComponent(l, WithPhotoStorageSingle(context.Background()), name, []byte(name))
			errs <- e
		}(name)
	}
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	if b.singles != 2 || b.batches != 0 {
		t.Fatal("retry did not isolate backend writes")
	}
}

func TestPhotoStreamsOwnerRevocationStopsBatch(t *testing.T) {
	l, b := componentFixture(t)
	owner, revoke := context.WithCancel(context.Background())
	defer revoke()
	l.SetPreviewLease(func(parent context.Context) (context.Context, func(), bool) {
		ctx, cancel := context.WithCancel(parent)
		stop := context.AfterFunc(owner, cancel)
		return ctx, func() { stop(); cancel() }, owner.Err() == nil
	})
	entered := make(chan struct{})
	b.before = func(ctx context.Context, _ []StreamInput) error { close(entered); <-ctx.Done(); return ctx.Err() }
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
	}
	<-entered
	revoke()
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := storeComponent(l, context.Background(), "late", []byte("late")); !errors.Is(err, context.Canceled) {
		t.Fatalf("revoked owner admitted: %v", err)
	}
}

func TestPhotoStreamsLockKeepsKeyUntilReadersReturn(t *testing.T) {
	l, b := componentFixture(t)
	entered, canceled, unblock := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b.before = func(ctx context.Context, _ []StreamInput) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-unblock
		if !l.vault.Unlocked() {
			t.Error("vault key cleared while backend still owns readers")
		}
		return ctx.Err()
	}
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
	}
	<-entered
	locked := make(chan error, 1)
	// This is the synchronous sequence used by filesvc.Resource.LockVault.
	go func() { l.PrepareVaultLock(); l.vault.Lock(); l.ForgetVaultSession(); locked <- nil }()
	<-canceled
	select {
	case <-locked:
		t.Fatal("lock returned before reader ownership ended")
	default:
	}
	close(unblock)
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if err := awaitComponent(t, locked); err != nil {
		t.Fatal(err)
	}
	if l.vault.Unlocked() {
		t.Fatal("vault remained unlocked")
	}
}
