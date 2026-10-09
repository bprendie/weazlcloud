package library

import (
	"context"
	"os/exec"
	"testing"
)

func TestMetadataPersistentReaderMatchesCatalogAndSeesLaterUploads(t *testing.T) {
	if _, err := exec.LookPath("weazl-restic-reader"); err != nil {
		t.Skip("metadata reader unavailable")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	l := newPhotoIndexTestLibrary(t)
	l.backend = newResticBackend(l.repo, l.vault)
	ctx, release := l.previewContext(context.Background())
	defer release()
	name := "Photos/a.jpg.json"
	body := []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`)
	if _, err := l.Put(ctx, name, body); err != nil {
		t.Fatal(err)
	}
	r, err := l.newMetadataResolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.openReader(ctx)
	defer r.closeReader()
	if r.reader == nil {
		t.Skip("resource budget uses CLI fallback")
	}
	shared, done := l.borrowPreviewReader(ctx)
	if shared != r.reader {
		t.Fatal("thumbnail and metadata reader sessions diverged")
	}
	done()
	file, err := l.Metadata(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.readPersistent(ctx, file, 4<<20)
	if err != nil || string(got) != string(body) {
		t.Fatal("persistent source mismatch", err)
	}
	// A source created after index load refreshes the same authenticated reader.
	newBody := []byte(`{"photoTakenTime":{"timestamp":"1565152400"}}`)
	if _, err = l.Put(ctx, "Photos/new.json", newBody); err != nil {
		t.Fatal(err)
	}
	newFile, err := l.Metadata(ctx, "Photos/new.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err = r.readPersistent(ctx, newFile, 4<<20)
	if err != nil || string(got) != string(newBody) {
		t.Fatal("resident reader did not refresh new upload", err)
	}
	if err = l.Rename(ctx, name, "Photos/renamed.json"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.readPersistent(ctx, file, 4<<20); err == nil {
		t.Fatal("stale catalog reference accepted")
	}
	l.vault.Lock()
	if _, err = r.readPersistent(ctx, newFile, 4<<20); err == nil {
		t.Fatal("locked vault read accepted")
	}
}
