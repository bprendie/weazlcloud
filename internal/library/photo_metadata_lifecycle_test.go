package library

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type metadataBlockingBackend struct {
	Backend
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *metadataBlockingBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	blocked := false
	b.once.Do(func() { blocked = true; close(b.entered) })
	if blocked {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.release:
		}
	}
	return b.Backend.Read(ctx, ref, w)
}
func TestMetadataPauseAndRestartResumePendingEncryptedWork(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	_, _ = l.Put(ctx, "Photos/a.png", []byte("original"))
	_, _ = l.Put(ctx, "Photos/a.png.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`))
	backend := &metadataBlockingBackend{Backend: l.backend, entered: make(chan struct{}), release: make(chan struct{})}
	l.backend = backend
	_, err := l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	paused, err := l.SetPhotoMetadataJob(ctx, "pause", PhotoMetadataOptionsJob{})
	if err != nil || paused.Status != "paused" {
		t.Fatal(paused, err)
	}
	close(backend.release)
	drainCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err = l.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = backend
	restarted.photoAutoDisabled = true
	t.Cleanup(func() { stopPhotoIndexSaveForTest(restarted) })
	state, err := restarted.PhotoMetadataStatus()
	if err != nil || state.Status != "paused" || state.Examined != 0 {
		t.Fatal("lost checkpoint", state, err)
	}
	_, err = restarted.SetPhotoMetadataJob(ctx, "resume", PhotoMetadataOptionsJob{})
	if err != nil {
		t.Fatal(err)
	}
	state = waitMetadata(t, restarted)
	if state.Updated != 1 || state.Failed != 0 {
		t.Fatal(state)
	}
	body, err := restarted.Get(ctx, "Photos/a.png")
	if err != nil || string(body) != "original" {
		t.Fatal("original changed", err)
	}
}
