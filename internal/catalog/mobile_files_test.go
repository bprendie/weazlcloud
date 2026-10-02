package catalog

import (
	"errors"
	"testing"
)

func TestMobileFilesStableLookupAndBoundedSnapshot(t *testing.T) {
	c := testCatalog(t)
	for _, name := range []string{"Docs/a", "Docs/b", "Docs-extra/c"} {
		if err := c.Put(File{Path: name, Snap: name, Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	original, _ := c.Get("Docs/a")
	if err := c.Rename("Docs/a", "Docs/renamed"); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(File{Path: "Docs/a", Snap: "replacement", Present: true}); err != nil {
		t.Fatal(err)
	}
	got, err := c.MobileFile(original.EntryID)
	if err != nil || got.Path != "Docs/renamed" || got.Revision != original.Revision+1 {
		t.Fatalf("lookup=%+v err=%v", got, err)
	}
	got.Reference.Object = "mutated clone"
	if again, _ := c.MobileFile(original.EntryID); again.Reference.Object == got.Reference.Object {
		t.Fatal("reference alias")
	}
	seen := map[string]bool{}
	after := ""
	for {
		rows, more := c.MobileFilesSnapshot(after, "Docs", 1)
		if len(rows) > 1 {
			t.Fatal("unbounded page")
		}
		for _, f := range rows {
			if seen[f.EntryID] {
				t.Fatal("duplicate ID")
			}
			seen[f.EntryID] = true
			after = f.EntryID
		}
		if !more {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatalf("seen=%v", seen)
	}
	if err := c.Delete("Docs/renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MobileFile(original.EntryID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted lookup=%v", err)
	}
	if err := c.Put(File{Path: ".weazl-mobile-pending/private", Snap: "private", Present: true}); err != nil {
		t.Fatal(err)
	}
	private, _ := c.Get(".weazl-mobile-pending/private")
	if _, err := c.MobileFile(private.EntryID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private lookup=%v", err)
	}
	rows, _ := c.MobileFilesSnapshot("", "", 200)
	for _, f := range rows {
		if f.EntryID == private.EntryID {
			t.Fatal("private staging in snapshot")
		}
	}
}
