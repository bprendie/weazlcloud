package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMetadataLateSidecarCatalogOutboxSurvivesLostInMemoryQueue(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	l.photoAutoDisabled = false
	// Simulate a process disappearing between the catalog commit and the end
	// of a ZIP import. Its in-memory directory queue must not be the authority.
	l.BeginPhotoStorageWork()
	ctx := context.Background()
	if _, err := l.Put(ctx, "Photos/a.png", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/a.png.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Photos/a.png", "Photos/a.png.json"} {
		file, err := l.Metadata(ctx, name)
		if err != nil || !file.PhotoProcessingPending {
			t.Fatal("missing durable marker", name, err)
		}
	}
	drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := l.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = l.backend
	restarted.photoPrep.Paused = true
	restarted.photoPrep.Status = "paused"
	t.Cleanup(func() { stopPhotoIndexSaveForTest(restarted) })
	if _, err := restarted.Metadata(ctx, "Photos/a.png"); err != nil {
		t.Fatal(err)
	}
	state := waitMetadata(t, restarted)
	if state.Updated != 1 || state.Failed != 0 {
		t.Fatal(state)
	}
	photo, _ := restarted.Metadata(ctx, "Photos/a.png")
	if photo.CaptureTime == nil || photo.CaptureTime.Year() != 2013 {
		t.Fatal("late sidecar was not replayed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		file, err := restarted.Metadata(ctx, "Photos/a.png.json")
		if err != nil {
			t.Fatal(err)
		}
		if !file.PhotoProcessingPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable outbox was not acknowledged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	body, err := restarted.Get(ctx, "Photos/a.png")
	if err != nil || string(body) != "original" {
		t.Fatal("original changed", err)
	}
}
