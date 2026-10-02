package library

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type metadataConcurrentBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (b *metadataConcurrentBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	current := b.active.Add(1)
	defer b.active.Add(-1)
	for {
		old := b.peak.Load()
		if old >= current || b.peak.CompareAndSwap(old, current) {
			break
		}
	}
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.Backend.Read(ctx, ref, w)
}

func TestMetadataParallelReadsAndCanceledBatchRetainsPending(t *testing.T) {
	if metadataWorkers() < 2 {
		t.Skip("one-worker resource policy")
	}
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	count := metadataWorkers() + 2
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("Photos/%d.png", i)
		if _, err := l.Put(ctx, name, []byte("original")); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Put(ctx, name+".json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`)); err != nil {
			t.Fatal(err)
		}
	}
	b := &metadataConcurrentBackend{Backend: l.backend, entered: make(chan struct{}, count*2), release: make(chan struct{})}
	l.backend = b
	if _, err := l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < metadataWorkers(); i++ {
		select {
		case <-b.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("metadata reads are serialized")
		}
	}
	if _, err := l.SetPhotoMetadataJob(ctx, "pause", PhotoMetadataOptionsJob{}); err != nil {
		t.Fatal(err)
	}
	l.metadataMu.Lock()
	done := l.metadataDone
	l.metadataMu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("parallel pause did not drain")
	}
	state, err := l.PhotoMetadataStatus()
	if err != nil || state.Examined != 0 || state.Status != "paused" {
		t.Fatal("canceled batch lost pending work", state, err)
	}
	close(b.release)
	if _, err = l.SetPhotoMetadataJob(ctx, "resume", PhotoMetadataOptionsJob{}); err != nil {
		t.Fatal(err)
	}
	state = waitMetadata(t, l)
	if state.Updated != count || state.Failed != 0 || b.peak.Load() > int32(metadataWorkers()) {
		t.Fatal("parallel result mismatch", state, b.peak.Load())
	}
}
