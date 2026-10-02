package library

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type slowFirstPreviewBackend struct {
	*isolatedLegacy
	started chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func (b *slowFirstPreviewBackend) Read(ctx context.Context, ref catalog.Reference, out io.Writer) error {
	if b.first.CompareAndSwap(false, true) {
		close(b.started)
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.isolatedLegacy.Read(ctx, ref, out)
}

func TestPhotoQueueRefillsWhileOneSourceIsSlow(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	b := &slowFirstPreviewBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "library")}, started: make(chan struct{}), release: make(chan struct{})}
	l.backend = b
	old := previewPolicy.BackgroundWorkers
	previewPolicy.BackgroundWorkers = 2
	defer func() { previewPolicy.BackgroundWorkers = old }()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		body := append(append([]byte(nil), imageBytes.Bytes()...), byte(i))
		if _, err := l.Put(context.Background(), "Photos/queue-"+string(rune('a'+i))+".png", body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	defer func() { close(b.release); waitPreparation(t, l, "complete") }()
	select {
	case <-b.started:
	case <-time.After(3 * time.Second):
		t.Fatal("source never started")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, _, ready, _, err := l.photoJobCounts()
		if err != nil {
			t.Fatal(err)
		}
		if ready == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fast workers waited for the slow source before taking remaining jobs")
}
