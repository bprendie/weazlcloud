package catalog

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPhotoAlbumsPersistEncryptedWithRevisionedMembership(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/Trip/photo.jpg", Size: 4, Mtime: time.Now().UTC(), Present: true}); err != nil {
		t.Fatal(err)
	}
	file, ok := c.Get("Photos/Trip/photo.jpg")
	if !ok {
		t.Fatal("photo missing")
	}
	album, err := c.SaveAlbum(Album{Title: "Secret summer", Description: "private caption", AssetIDs: []string{file.EntryID}, CoverID: file.EntryID})
	if err != nil {
		t.Fatal(err)
	}
	if album.ID == "" || album.Revision != 1 {
		t.Fatalf("created album=%+v", album)
	}
	// Metadata-only edits may choose an existing member without resending the
	// entire membership list.
	if _, err := c.SaveAlbum(Album{ID: album.ID, Revision: album.Revision, Title: "Secret summer", CoverID: file.EntryID}); err != nil {
		t.Fatal(err)
	}
	album.Revision++
	if _, err = c.SaveAlbum(Album{ID: album.ID, Revision: album.Revision, Title: "Summer", AssetIDs: []string{file.EntryID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveAlbum(Album{ID: album.ID, Revision: 1, Title: "Stale", AssetIDs: []string{file.EntryID}}); err != ErrRevisionMismatch {
		t.Fatalf("stale revision=%v", err)
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Secret summer") || strings.Contains(string(raw), "private caption") {
		t.Fatal("album text leaked outside encrypted catalog")
	}
	reloaded := New(c.path, c.vault)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := reloaded.Albums()
	if len(got) != 1 || got[0].Title != "Summer" || got[0].Revision != 3 || len(got[0].AssetIDs) != 1 {
		t.Fatalf("persisted albums=%+v", got)
	}
	if err := reloaded.DeleteAlbum(got[0].ID, got[0].Revision); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Albums()) != 0 {
		t.Fatal("album still present after delete")
	}
}
