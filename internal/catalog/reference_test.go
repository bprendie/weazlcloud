package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func writeCatalogFixture(t *testing.T, c *Catalog, files []File) []byte {
	t.Helper()
	plain, err := json.Marshal(tree{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.vault.Wrap(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := cryptox.AtomicWrite(c.path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestLegacyCatalogUpgradeAssignsStableVersionedReferences(t *testing.T) {
	c := testCatalog(t)
	deleted := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	writeCatalogFixture(t, c, []File{
		{Path: "folder", Folder: true, Present: true},
		{Path: "folder/item.bin", Snap: "snapshot-a", Object: "/old/batch/folder/item.bin", Hash: "sha-a", Present: true},
		{Path: "same.txt", Snap: "snapshot-b", Object: "", Hash: "sha-b", Present: false, DeletedAt: &deleted},
		{Path: "same.txt", Snap: "snapshot-c", Object: "object-c", Hash: "sha-c", Present: true},
	})
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	first := c.All()
	if len(first) != 4 {
		t.Fatalf("entries=%d", len(first))
	}
	ids := make(map[string]bool)
	for _, file := range first {
		if file.EntryID == "" || file.Revision != 1 || ids[file.EntryID] {
			t.Fatalf("identity missing or duplicated: %+v", file)
		}
		ids[file.EntryID] = true
	}
	batch := first[1]
	if batch.Snap != "snapshot-a" || batch.Object != "/old/batch/folder/item.bin" || batch.Reference == nil || batch.Reference.Backend != ResticBackend || batch.Reference.Version != 1 || batch.Reference.Snapshot != batch.Snap || batch.Reference.Object != batch.Object {
		t.Fatalf("legacy batch reference changed: %+v", batch)
	}
	if first[2].Reference == nil || first[2].Reference.Object != "sha-b" || first[2].Object != "" {
		t.Fatalf("empty legacy object fallback changed source fields: %+v", first[2])
	}
	if first[2].EntryID == first[3].EntryID {
		t.Fatal("live and trashed versions at one path share an identity")
	}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	second := c.All()
	for i := range first {
		if first[i].EntryID != second[i].EntryID || first[i].Revision != second[i].Revision {
			t.Fatalf("identity changed after reload: before=%+v after=%+v", first[i], second[i])
		}
	}
}

func TestUnknownCatalogReferenceFailsWithoutRewrite(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "previous.txt", Snap: "previous-snapshot", Object: "previous-object"}); err != nil {
		t.Fatal(err)
	}
	original := writeCatalogFixture(t, c, []File{{
		EntryID: "fixed-entry", Revision: 4, Path: "future.bin", Snap: "future-snapshot", Object: "future-object", Present: true,
		Reference: &Reference{Backend: "shared-object", Version: 2, Snapshot: "opaque-id", Object: "manifest-id"},
	}})
	if err := c.Load(); !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("unknown reference error=%v", err)
	}
	got, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("unsupported catalog was rewritten")
	}
	if len(c.All()) != 0 {
		t.Fatal("unsupported catalog was published in memory")
	}
}

func TestCatalogEntryRevisionAndReplacementIdentity(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "same.txt", Snap: "snap-one", Object: "object-one", Hash: "hash-one"}); err != nil {
		t.Fatal(err)
	}
	first, _ := c.Get("same.txt")
	if err := c.Rename("same.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	renamed, _ := c.Get("renamed.txt")
	if renamed.EntryID != first.EntryID || renamed.Revision != first.Revision+1 {
		t.Fatalf("rename identity/revision: before=%+v after=%+v", first, renamed)
	}
	if err := c.Delete("renamed.txt"); err != nil {
		t.Fatal(err)
	}
	trashed := c.Trash()[0]
	if trashed.EntryID != first.EntryID || trashed.Revision != renamed.Revision+1 {
		t.Fatalf("trash identity/revision: %+v", trashed)
	}
	if err := c.Put(File{Path: "renamed.txt", Snap: "snap-two", Object: "object-two", Hash: "hash-two"}); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Get("renamed.txt")
	if live.EntryID == trashed.EntryID || live.Revision != 1 {
		t.Fatalf("replacement reused old identity: trashed=%+v live=%+v", trashed, live)
	}
	if err := c.Restore("renamed.txt"); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore conflict=%v", err)
	}
}

func TestRestoreAdvancesTheExistingEntryRevision(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "restore.txt", Snap: "restore-snapshot", Object: "restore-object"}); err != nil {
		t.Fatal(err)
	}
	initial, _ := c.Get("restore.txt")
	if err := c.Delete("restore.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.Restore("restore.txt"); err != nil {
		t.Fatal(err)
	}
	restored, _ := c.Get("restore.txt")
	if restored.EntryID != initial.EntryID || restored.Revision != initial.Revision+2 {
		t.Fatalf("restore identity/revision: before=%+v after=%+v", initial, restored)
	}
}
