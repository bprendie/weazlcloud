package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestSavedPhotoSelectionIsEncryptedScopedAndRevalidated(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg", "Photos/Private/secret.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	selection, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{Mode: "all"})
	if err != nil || selection.Count != 2 {
		t.Fatalf("selection=%+v %v", selection, err)
	}
	raw, err := os.ReadFile(filepath.Join(l.photoSelectionDir(), selection.ID+".enc"))
	if err != nil || bytes.Contains(raw, []byte("entry_id")) || bytes.Contains(raw, []byte("revision")) {
		t.Fatalf("plaintext manifest: %v", err)
	}
	other := newPhotoIndexTestLibrary(t)
	if _, err := other.ResolvePhotoSelection(ctx, selection.ID, false); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("foreign selection accepted: %v", err)
	}
	if _, err := l.ResolvePhotoSelection(ctx, selection.ID, true); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("hidden scope changed: %v", err)
	}
	album, err := l.SavePhotoSelectionAlbum(ctx, selection.ID, false, catalog.Album{Title: "All visible"})
	if err != nil || len(album.AssetIDs) != 2 {
		t.Fatalf("selection album=%+v %v", album, err)
	}
	manifest, err := l.PreparePhotoSelectionArchive(ctx, selection.ID, false)
	if err != nil || manifest.Files != 2 {
		t.Fatalf("selection archive=%+v %v", manifest, err)
	}
	manifest.Release()
	exports, release, err := l.PreparePhotoSelectionExport(ctx, selection.ID, false)
	if err != nil || len(exports) != 2 {
		t.Fatalf("selection export=%+v %v", exports, err)
	}
	release()
	if err := l.Rename(ctx, "Photos/a.jpg", "Photos/Private/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PreparePhotoSelectionArchive(ctx, selection.ID, false); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("hidden move did not invalidate archive: %v", err)
	}
	if _, err := l.DeletePhotoSelection(ctx, selection.ID, false); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("stale selection partially deleted files: %v", err)
	}
	if _, ok := l.catalog.Get("Photos/b.jpg"); !ok {
		t.Fatal("rejected action changed unaffected file")
	}
	hidden, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{Mode: "hidden"})
	if err != nil || hidden.Count != 2 {
		t.Fatalf("hidden selection=%+v %v", hidden, err)
	}
	count, err := l.DeletePhotoSelection(ctx, hidden.ID, true)
	if err != nil || count != 2 {
		t.Fatalf("hidden delete=%d %v", count, err)
	}
	if trash, err := l.PhotoTrash(ctx, true); err != nil || len(trash) != 2 {
		t.Fatalf("hidden trash=%+v %v", trash, err)
	}
}
