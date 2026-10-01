package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetadataBatchSingleSaveConflictAndFailure(t *testing.T) {
	c := testCatalog(t)
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg"} {
		if err := c.Put(File{Path: name, Size: 4, Hash: name, Snap: "snapshot", Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := c.Get("Photos/a.jpg")
	second, _ := c.Get("Photos/b.jpg")
	captured := time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC)
	update := func(file File) PhotoMetadataMutation {
		return PhotoMetadataMutation{ID: file.EntryID, Path: file.Path, Hash: file.Hash, Revision: file.Revision, Capture: &CaptureMetadata{Time: &captured, Source: "test"}}
	}
	version := c.Version()
	result, err := c.UpdatePhotoMetadataBatch([]PhotoMetadataMutation{update(first), update(second)})
	if err != nil || len(result.Changed) != 2 || c.Version() != version+1 {
		t.Fatalf("batch %+v %v version %d", result, err, c.Version())
	}
	stale, err := c.UpdatePhotoMetadataBatch([]PhotoMetadataMutation{update(first)})
	if err != nil || len(stale.Conflicts) != 1 || c.Version() != version+1 {
		t.Fatal("stale write", stale, err)
	}
	first, _ = c.Get(first.Path)
	originalPath := c.path
	c.path = filepath.Join(t.TempDir(), "directory")
	_ = os.Mkdir(c.path, 0700)
	later := captured.AddDate(1, 0, 0)
	mutation := update(first)
	mutation.Capture.Time = &later
	if _, err = c.UpdatePhotoMetadataBatch([]PhotoMetadataMutation{mutation}); err == nil {
		t.Fatal("expected persistence failure")
	}
	got, _ := c.Get(first.Path)
	if !got.CaptureTime.Equal(captured) || got.Revision != first.Revision {
		t.Fatal("failed save changed authority", got)
	}
	c.path = originalPath
}
