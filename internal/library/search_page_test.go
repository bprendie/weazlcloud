package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestSearchPageHandlesWildcardsFiltersAndCursors(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("search-pass"), []byte("search-pass")); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.enc")
	c := catalog.New(catalogPath, v)
	for _, name := range []string{"Photos/Summer/family-one.jpg", "Photos/Winter/family-two.jpg", "Docs/guide.pdf"} {
		if err := c.Put(catalog.File{Path: name, Size: 2 << 20, Hash: name, Mtime: time.Now(), Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	l := New(filepath.Join(root, "repo"), catalogPath, v)
	l.backend = &isolatedLegacy{root: filepath.Join(root, "repo")}
	options := SearchOptions{Query: "family-*.jpg", Scope: "Photos", Type: "image", Date: "30d", Size: "medium", Sort: "name", Limit: 1}
	first, err := l.SearchPage(context.Background(), options)
	if err != nil || len(first.Files) != 1 || first.NextCursor == "" {
		t.Fatalf("first search page=%+v err=%v", first, err)
	}
	options.Cursor = first.NextCursor
	second, err := l.SearchPage(context.Background(), options)
	if err != nil || len(second.Files) != 1 || first.Files[0].Path == second.Files[0].Path {
		t.Fatalf("second search page=%+v err=%v", second, err)
	}
	options.Query = "guide*"
	options.Type, options.Scope = "document", "Docs"
	options.Cursor = ""
	filtered, err := l.SearchPage(context.Background(), options)
	if err != nil || len(filtered.Files) != 1 || filtered.Files[0].Path != "Docs/guide.pdf" {
		t.Fatalf("filtered page=%+v err=%v", filtered, err)
	}
}
