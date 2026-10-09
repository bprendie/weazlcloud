package library

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoStreamsNativeStockReadAndRange(t *testing.T) {
	if _, err := exec.LookPath("weazl-restic-writer"); err != nil {
		t.Skip("native writer unavailable")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	l, _ := componentFixture(t)
	l.backend = newResticBackend(l.repo, l.vault)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 4)
	files := make(chan catalog.File, 4)
	for _, name := range []string{"one", "two", "three", "four"} {
		go func(name string) {
			body := bytes.Repeat([]byte(name), 4096)
			f, e := storeComponent(l, context.Background(), name, body)
			if e == nil {
				files <- f
			}
			errs <- e
		}(name)
	}
	for i := 0; i < 4; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot string
	for i := 0; i < 4; i++ {
		f := <-files
		if snapshot == "" {
			snapshot = f.Reference.Snapshot
		}
		if f.Reference.Snapshot != snapshot {
			t.Fatal("native helper did not batch")
		}
		name := strings.TrimPrefix(f.Path, ".weazl-mobile-pending/")
		want := bytes.Repeat([]byte(name), 4096)
		var got bytes.Buffer
		if err := l.backend.Read(context.Background(), *f.Reference, &got); err != nil || !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("stock restic dump: %v", err)
		}
		got.Reset()
		if err := l.backend.ReadRange(context.Background(), *f.Reference, 7, 123, &got); err != nil || !bytes.Equal(got.Bytes(), want[7:130]) {
			t.Fatalf("range: %v", err)
		}
	}
	singles, batches := l.ResticCommitCounts()
	if singles != 0 || batches != 1 {
		t.Fatalf("commits: %d %d", singles, batches)
	}
}

type componentPurgeBackend struct {
	*componentBackend
	snapshot  string
	forgotten []string
}

func (b *componentPurgeBackend) Snapshots(context.Context) ([]string, error) {
	return []string{b.snapshot}, nil
}
func (b *componentPurgeBackend) Forget(_ context.Context, ids []string) ([]string, error) {
	b.forgotten = append(b.forgotten, ids...)
	return nil, nil
}

func TestPhotoComponentDeferredPruneRechecksPublication(t *testing.T) {
	l, base := componentFixture(t)
	ctx := context.Background()
	f, err := storeComponent(l, ctx, "one", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := strings.Repeat("a", 64)
	ref := resticReference(snapshot, f.EntryID, "")
	f.Reference = &ref
	f.Snap = snapshot
	if err := l.catalog.DiscardPhotoPending([]catalog.PhotoIngestFile{{From: f.Path, Size: f.Size, Hash: f.Hash}}); err != nil {
		t.Fatal(err)
	}
	b := &componentPurgeBackend{componentBackend: base, snapshot: snapshot}
	l.backend = b
	p, err := l.photoComponentIntentPath(f.Path, f.Size, f.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.savePhotoComponentIntent(p, photoComponentIntent{Version: 1, File: f}); err != nil {
		t.Fatal(err)
	}
	if err := l.saveTrashIntent(trashCleanupIntent{Version: 1, CatalogPurged: true, Snapshots: []string{snapshot}}); err != nil {
		t.Fatal(err)
	}
	if err := l.resumeTrashCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.forgotten) != 0 {
		t.Fatal("pruned intent-only original")
	}
	if err := l.catalog.Put(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := l.resumeTrashCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.forgotten) != 0 {
		t.Fatal("pruned newly published original")
	}
	if err := l.catalog.DiscardPhotoPending([]catalog.PhotoIngestFile{{From: f.Path, Size: f.Size, Hash: f.Hash}}); err != nil {
		t.Fatal(err)
	}
	if err := l.resumeTrashCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.forgotten) != 1 {
		t.Fatal("unreferenced snapshot was not reclaimed")
	}
}
