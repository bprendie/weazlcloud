package catalog

import (
	"errors"
	"path/filepath"
	"testing"

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
