package library

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestFolderPagesAreBoundedSortedAndGenerationBound(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("page-pass"), []byte("page-pass")); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.enc")
	c := catalog.New(catalogPath, v)
	for _, name := range []string{"z.txt", "a.txt", "m.txt"} {
		if err := c.Put(catalog.File{Path: name, Size: 1, Hash: name, Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	l := New(filepath.Join(root, "repo"), catalogPath, v)
	l.backend = &isolatedLegacy{root: filepath.Join(root, "repo")}
	first, err := l.ListFolderPage(context.Background(), "", "name", false, 2, "")
	if err != nil || len(first.Files) != 2 || first.Files[0].Path != "a.txt" || first.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	if err := l.Mkdir(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ListFolderPage(context.Background(), "", "name", false, 2, first.NextCursor); err != ErrFolderCursor {
		t.Fatalf("stale cursor error=%v", err)
	}
}
