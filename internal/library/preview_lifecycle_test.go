package library

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreviewOwnerRevocationCancelsAndDrains(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	f := putPreviewFixture(t, l, "Photos/a.png")
	owner, revoke := context.WithCancel(context.Background())
	l.SetPreviewLease(func(parent context.Context) (context.Context, func(), bool) {
		ctx, cancel := context.WithCancel(parent)
		stop := context.AfterFunc(owner, cancel)
		return ctx, func() { stop(); cancel() }, owner.Err() == nil
	})
	b := &blockingPreviewBackend{Backend: l.backend, started: make(chan struct{}), proceed: make(chan struct{})}
	l.backend = b
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := l.Thumbnail(ctx, f.Path, 320); done <- err }()
	select {
	case <-b.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	revoke()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked preview returned")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := l.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Thumbnail(ctx, f.Path, 320); err == nil {
		t.Fatal("drained library admitted preview")
	}
}

func TestPreviewRekeyInvalidatesPreviousSession(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	f := putPreviewFixture(t, l, "Photos/a.png")
	ctx, release := l.previewContext(context.Background())
	defer release()
	if err := l.vault.Rekey([]byte("photo-page-test"), []byte("new-test-pass"), []byte("new-test-pass")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(l.validatePreview(ctx, f), context.Canceled) {
		t.Fatal("old session survived rekey")
	}
	if _, _, err := l.Thumbnail(context.Background(), f.Path, 320); err != nil {
		t.Fatalf("new session failed: %v", err)
	}
}

func TestPhotoPreparationDrainDuringBlockedSource(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	b := &blockingPreviewBackend{Backend: l.backend, started: make(chan struct{}), proceed: make(chan struct{})}
	l.backend = b
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.SetPhotoPreparation(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := l.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	l.photoPrepMu.Lock()
	running, ready, failed := l.photoPrepRunning, l.photoPrep.Ready, l.photoPrep.Failed
	l.photoPrepMu.Unlock()
	if running || ready != 0 || failed != 0 {
		t.Fatalf("cancelled work counted: running=%v ready=%d failed=%d", running, ready, failed)
	}
}
