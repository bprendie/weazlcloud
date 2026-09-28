package catalog

import (
	"os"
	"testing"
)

func TestChildrenReturnsDirectRowsAndSynthesizesLegacyFolders(t *testing.T) {
	c := testCatalog(t)
	c.files = []File{
		{Path: "Photos/Album/a.jpg", Present: true},
		{Path: "Photos/root.png", Present: true},
		{Path: "Docs", Folder: true, EntryID: "folder-id", Present: true},
	}
	c.children = indexChildren(c.files)
	root := c.Children("")
	if len(root) != 2 {
		t.Fatalf("root children = %d, want 2: %+v", len(root), root)
	}
	var photos File
	for _, row := range root {
		if row.Path == "Photos" {
			photos = row
		}
	}
	if !photos.Folder || photos.EntryID != "" {
		t.Fatalf("legacy folder not synthesized: %+v", photos)
	}
	children := c.Children("Photos")
	if len(children) != 2 {
		t.Fatalf("Photos children = %d, want 2: %+v", len(children), children)
	}
	children[0].Path = "modified"
	if c.Children("Photos")[0].Path == "modified" {
		t.Fatal("caller mutated the cached catalog index")
	}
}

func TestDiskUnchangedDetectsExternalCatalogWrite(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "file.txt", Size: 3, Hash: "abc", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	if !c.DiskUnchanged() {
		t.Fatal("unchanged catalog reported modified")
	}
	if err := os.WriteFile(c.path, []byte("external change"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c.DiskUnchanged() {
		t.Fatal("external catalog write was not detected")
	}
}
