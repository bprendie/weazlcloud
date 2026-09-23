package catalog

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("catalog-pass"), []byte("catalog-pass")); err != nil {
		t.Fatal(err)
	}
	return New(filepath.Join(dir, "catalog.enc"), v)
}

func TestCatalogDeleteRestoreAndPurgeTrash(t *testing.T) {
	c := testCatalog(t)
	if err := c.Mkdir("photos"); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(File{Path: "photos/a.jpg", Size: 12, Snap: "snap-a", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Copy("photos", "backup"); err != nil {
		t.Fatal(err)
	}
	original, ok := c.Get("photos/a.jpg")
	if !ok {
		t.Fatal("source file missing after copy")
	}
	copied, ok := c.Get("backup/a.jpg")
	if !ok {
		t.Fatal("copy did not preserve the file")
	}
	if copied.EntryID == original.EntryID || copied.Revision != 1 {
		t.Fatalf("copy did not get a new identity: original=%+v copied=%+v", original, copied)
	}
	if err := c.Copy("photos", "backup"); !errors.Is(err, ErrConflict) {
		t.Fatalf("copy collision: %v", err)
	}
	if err := c.Delete("photos"); err != nil {
		t.Fatal(err)
	}
	trash := c.Trash()
	if len(trash) != 2 || trash[1].DeletedAt == nil {
		t.Fatalf("trash=%+v", trash)
	}
	if err := c.Restore("photos"); err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 4 || len(c.Trash()) != 0 {
		t.Fatalf("restored list=%+v trash=%+v", c.List(), c.Trash())
	}
	if err := c.Delete("photos/a.jpg"); err != nil {
		t.Fatal(err)
	}
	removed, err := c.PurgeTrash(time.Now().UTC().Add(time.Minute))
	if err != nil || len(removed) != 1 || removed[0].Path != "photos/a.jpg" {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
}

func TestCatalogRejectsFileFolderCollisions(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "docs", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir("docs"); !errors.Is(err, ErrConflict) {
		t.Fatalf("file to folder collision: %v", err)
	}
	if err := c.Put(File{Path: "docs/readme", Present: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("child below file collision: %v", err)
	}
}

func TestReplacementAtTrashedPathPreservesTrashAndRestoreConflicts(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "archive/item.bin", Size: 3, Hash: "old", Snap: "old-snap", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("archive/item.bin"); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(File{Path: "archive/item.bin", Size: 4, Hash: "new", Snap: "new-snap", Present: true}); err != nil {
		t.Fatal(err)
	}
	if len(c.Trash()) != 1 || c.Trash()[0].Hash != "old" {
		t.Fatalf("replacement discarded trashed entry: %+v", c.Trash())
	}
	live, ok := c.Get("archive/item.bin")
	if !ok || live.Hash != "new" {
		t.Fatalf("replacement is not the live version: %+v, %v", live, ok)
	}
	if err := c.Restore("archive/item.bin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore over replacement should conflict, got %v", err)
	}
	if len(c.List()) != 1 || c.List()[0].Hash != "new" {
		t.Fatalf("failed restore introduced duplicate live paths: %+v", c.List())
	}
}

func TestCatalogRejectsUnsafeRenameWithoutMutating(t *testing.T) {
	c := testCatalog(t)
	if err := c.Mkdir("photos"); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir("photos"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate folder: %v", err)
	}
	if err := c.Put(File{Path: "photos/a.jpg", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename("photos", "photos/archive"); !errors.Is(err, ErrDescendant) {
		t.Fatalf("descendant rename: %v", err)
	}
	if _, ok := c.Get("photos/a.jpg"); !ok {
		t.Fatal("failed rename changed the catalog")
	}
	if err := c.Mkdir("archive"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename("photos", "archive"); !errors.Is(err, ErrConflict) {
		t.Fatalf("destination collision: %v", err)
	}
	if _, ok := c.Get("photos/a.jpg"); !ok {
		t.Fatal("collision changed the catalog")
	}
}

func TestCatalogSaveFailureDoesNotPublishMemoryState(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "before.txt", Size: 6, Hash: "before", Present: true}); err != nil {
		t.Fatal(err)
	}
	// A directory at the target path makes the final atomic rename fail after
	// the encrypted temporary file has been written and synced.
	dir := filepath.Dir(c.path)
	c.path = dir
	if err := c.Put(File{Path: "after.txt", Size: 5, Hash: "after", Present: true}); err == nil {
		t.Fatal("catalog save unexpectedly succeeded")
	}
	if _, ok := c.Get("after.txt"); ok {
		t.Fatal("failed catalog save was published in memory")
	}
	if _, ok := c.Get("before.txt"); !ok {
		t.Fatal("committed catalog entry disappeared after failed save")
	}
}
