package library

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMetadataDurableFailurePausesAndRecoversWithoutFalseProgress(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		name := "catalog"
		if checkpoint {
			name = "checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			l := newPhotoIndexTestLibrary(t)
			ctx := context.Background()
			_, _ = l.Put(ctx, "Photos/a.png", []byte("original"))
			_, _ = l.Put(ctx, "Photos/a.png.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`))
			backend := &metadataBlockingBackend{Backend: l.backend, entered: make(chan struct{}), release: make(chan struct{})}
			l.backend = backend
			if _, err := l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-backend.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not start")
			}
			blocked := l.catalogPathForTest()
			if checkpoint {
				blocked = l.metadataJobPath()
			}
			if err := os.Rename(blocked, blocked+".backup"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(blocked, 0700); err != nil {
				t.Fatal(err)
			}
			close(backend.release)
			state := waitMetadata(t, l)
			if state.Status != "paused_error" || state.Examined != 0 {
				t.Fatal("false completion", state)
			}
			file, _ := l.Metadata(ctx, "Photos/a.png")
			if !checkpoint && file.CaptureTime != nil {
				t.Fatal("failed catalog commit changed authority")
			}
			if checkpoint && file.CaptureTime == nil {
				t.Fatal("expected commit before checkpoint failure")
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(blocked+".backup", blocked); err != nil {
				t.Fatal(err)
			}
			if _, err := l.SetPhotoMetadataJob(ctx, "resume", PhotoMetadataOptionsJob{}); err != nil {
				t.Fatal(err)
			}
			state = waitMetadata(t, l)
			if state.Examined != 1 || state.Failed != 0 {
				t.Fatal(state)
			}
			if checkpoint && state.Unchanged != 1 {
				t.Fatal("commit was not idempotently recovered", state)
			}
			body, err := l.Get(ctx, "Photos/a.png")
			if err != nil || string(body) != "original" {
				t.Fatal("original changed", err)
			}
		})
	}
}

func TestMetadataNewlyQueuedSidecarCannotLoseToOldCheckpoint(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	_, _ = l.Put(context.Background(), "Photos/a.png", []byte("original"))
	file, _ := l.Metadata(context.Background(), "Photos/a.png")
	entry := PhotoMetadataEntry{ID: file.EntryID, Path: file.Path, Hash: file.Hash, Revision: file.Revision, QueueVersion: 2, Status: "pending"}
	l.metadataMu.Lock()
	l.metadataJob = &PhotoMetadataJob{Version: 1, Parser: "capture-v2", Status: "running", Entries: []PhotoMetadataEntry{entry}}
	l.metadataMu.Unlock()
	old := entry
	old.QueueVersion = 1
	old.Status = "unresolved"
	if err := l.commitMetadataResults(context.Background(), []metadataResolved{{entry: old}}, true); err != nil {
		t.Fatal(err)
	}
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if l.metadataJob.Entries[0].Status != "pending" || l.metadataJob.Examined != 0 {
		t.Fatal("stale checkpoint consumed new work")
	}
}

func TestMetadataRenameResolvesStableIdentityWithinSelectedRoot(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	_, _ = l.Put(ctx, "Photos/a.png", []byte("original"))
	_, _ = l.Put(ctx, "Photos/a.png.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`))
	file, _ := l.Metadata(ctx, "Photos/a.png")
	entry := PhotoMetadataEntry{ID: file.EntryID, Path: file.Path, Hash: file.Hash, Revision: file.Revision, Status: "pending"}
	if err := l.Rename(ctx, file.Path, "Photos/renamed.png"); err != nil {
		t.Fatal(err)
	}
	if err := l.Rename(ctx, file.Path+".json", "Photos/renamed.png.json"); err != nil {
		t.Fatal(err)
	}
	resolver, err := l.newMetadataResolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result := l.resolveMetadataEntry(ctx, resolver, entry, PhotoMetadataOptionsJob{Root: "Photos"})
	if result.entry.Status != "updated" || result.entry.Path != "Photos/renamed.png" || result.entry.ID != entry.ID {
		t.Fatal(result.entry)
	}
	if err := l.Rename(ctx, "Photos/renamed.png", "Documents/a.png"); err != nil {
		t.Fatal(err)
	}
	result = l.resolveMetadataEntry(ctx, resolver, entry, PhotoMetadataOptionsJob{Root: "Photos"})
	if result.entry.Status != "failed" || result.mutation != nil {
		t.Fatal("asset outside selected root received a result")
	}
}
