package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestCollectionsNestingLegacyAndRollback(t *testing.T) {
	c := testCatalog(t)
	a, err := c.SaveAlbum(Album{Title: "Legacy"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := c.SaveCollectionFolder(CollectionFolder{Title: "Root"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := c.SaveCollectionFolder(CollectionFolder{Title: "Root", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	a.ParentID, a.ParentSet = child.ID, true
	a, err = c.SaveAlbum(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Title = "Browser rename"
	a.ParentID = ""
	a, err = c.SaveAlbum(a)
	if err != nil || a.ParentID != child.ID {
		t.Fatalf("legacy parent lost: %+v %v", a, err)
	}
	root.ParentID = child.ID
	if _, err = c.SaveCollectionFolder(root); !errors.Is(err, ErrDescendant) {
		t.Fatalf("cycle: %v", err)
	}
	if err = c.DeleteCollectionFolder(child.ID, child.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonempty: %v", err)
	}
	page, err := c.CollectionPage("", nil, 1)
	if err != nil || len(page.Nodes) != 1 || !page.HasMore {
		t.Fatalf("page: %+v %v", page, err)
	}
	child.Title = "Changed"
	child, err = c.SaveCollectionFolder(child)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CollectionPage(page.Next, &page.Position, 1); !errors.Is(err, ErrSyncExpired) {
		t.Fatal("stale snapshot accepted")
	}
	saved := c.path
	c.path = t.TempDir()
	child.ParentID = ""
	if _, err = c.SaveCollectionFolder(child); err == nil {
		t.Fatal("expected failed persistence")
	}
	c.path = saved
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	all, err := c.CollectionPage("", nil, 200)
	if err != nil || len(all.Nodes) != 3 {
		t.Fatalf("reload: %+v %v", all, err)
	}
	for _, n := range all.Nodes {
		if n.Folder != nil && n.ID == child.ID && n.Folder.ParentID != root.ID {
			t.Fatal("half move persisted")
		}
	}
	foreign := testCatalog(t)
	if _, err = foreign.SaveCollectionFolder(CollectionFolder{Title: "foreign", ParentID: root.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign parent: %v", err)
	}
	raw, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.vault.Unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var legacy struct {
		Files []File `json:"files"`
	}
	if json.Unmarshal(plain, &legacy) == nil {
		t.Fatal("legacy writer can discard schema 2")
	}
}
func TestSourceImportReplayConflictAndIndependentOperations(t *testing.T) {
	c := testCatalog(t)
	ops := []SourceOperation{
		{OperationID: "album", Namespace: "photokit", SourceID: "opaque/album", SourceRevision: "1", Kind: "album", ParentSourceID: "opaque/folder", Title: "Same"},
		{OperationID: "folder", Namespace: "photokit", SourceID: "opaque/folder", SourceRevision: "1", Kind: "folder", Title: "Same"},
		{OperationID: "bad", Namespace: "photokit", SourceID: "bad", SourceRevision: "1", Kind: "folder", Title: ""},
	}
	result, err := c.ImportSourceOperations("device", ops, false)
	if err != nil || result[0].Status != "applied" || result[1].Status != "applied" || result[2].Status != "invalid" {
		t.Fatalf("outcomes: %+v %v", result, err)
	}
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	retry, err := c.ImportSourceOperations("device", ops, false)
	if err != nil || retry[0] != result[0] {
		t.Fatalf("replay: %+v %v", retry, err)
	}
	ops[0].Title = "Changed"
	conflict, _ := c.ImportSourceOperations("device", ops[:1], false)
	if conflict[0].Code != "idempotency_conflict" {
		t.Fatal(conflict)
	}
	mapping, err := c.SourceMapping("device", "photokit", "opaque/album")
	if err != nil {
		t.Fatal(err)
	}
	album := c.Albums()[0]
	album.Title = "Server edit"
	if _, err = c.SaveAlbum(album); err != nil {
		t.Fatal(err)
	}
	ops[0].OperationID = "rename"
	ops[0].ExpectedRevision = mapping.ServerRevision
	conflict, _ = c.ImportSourceOperations("device", ops[:1], false)
	if conflict[0].Code != "stale_revision" {
		t.Fatal(conflict)
	}
	if _, err = c.SourceMapping("foreign-device", "photokit", "opaque/album"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign source exposed")
	}
}
func TestSourceKeyFraming(t *testing.T) {
	if SafeSourceKey("ab", "c") == SafeSourceKey("a", "bc") {
		t.Fatal("ambiguous framing")
	}
	if got := SafeSourceKey("photokit", "A/B+C==/L0/001"); got != "ytHTxxSsOndQxguM6jcAy19XNHi2TxPQDnRz1ktX-aM" {
		t.Fatalf("shared vector: %s", got)
	}
}

func TestCollectionLegacyUpgradeAndDepth(t *testing.T) {
	c := testCatalog(t)
	legacy := tree{Albums: []Album{{ID: "pa_legacy", Revision: 7, Title: "Legacy", AssetIDs: []string{"kept"}}}}
	plain, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.vault.Wrap(plain)
	clear(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	a := c.Albums()[0]
	if a.ID != "pa_legacy" || a.Revision != 7 || a.ParentID != "" || len(a.AssetIDs) != 1 {
		t.Fatalf("upgrade: %+v", a)
	}
	parent := ""
	for i := 0; i < 64; i++ {
		f, err := c.SaveCollectionFolder(CollectionFolder{Title: "Nested", ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		parent = f.ID
	}
	if _, err = c.SaveCollectionFolder(CollectionFolder{Title: "Too deep", ParentID: parent}); !errors.Is(err, ErrDescendant) {
		t.Fatalf("depth: %v", err)
	}
}
