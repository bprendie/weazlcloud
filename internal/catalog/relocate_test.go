package catalog

import (
	"os"
	"reflect"
	"testing"
)

func TestRelocatePreservesReferencesAndRejectsCollisions(t *testing.T) {
	c := testCatalog(t)
	for _, p := range []string{"Google Takeout", "Google Takeout/Drive", "Google Takeout/Drive/Trip"} {
		if err := c.Mkdir(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Put(File{Path: "Google Takeout/Drive/Trip /photo.jpg", Size: 12, Hash: "abc", Snap: "snapshot", Object: "old-object"}); err != nil {
		t.Fatal(err)
	}
	before, _ := c.Get("Google Takeout/Drive/Trip /photo.jpg")
	mapping := map[string]string{"Google Takeout": "", "Google Takeout/Drive": "", "Google Takeout/Drive/Trip": "Trip", "Google Takeout/Drive/Trip /photo.jpg": "Trip/photo.jpg"}
	raw, _ := os.ReadFile(c.path)
	if err := c.Relocate(mapping, false); err != nil {
		t.Fatal(err)
	}
	afterDry, _ := os.ReadFile(c.path)
	if !reflect.DeepEqual(raw, afterDry) {
		t.Fatal("dry run changed catalog")
	}
	if err := c.Relocate(mapping, true); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadReadOnly(); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("Trip/photo.jpg")
	before.Path = "Trip/photo.jpg"
	before.Revision++
	if !ok || !reflect.DeepEqual(got, before) {
		t.Fatalf("reference changed: %+v %+v", before, got)
	}
	if err := c.Mkdir("occupied"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(c.path)
	if err := c.Relocate(map[string]string{"Trip/photo.jpg": "occupied"}, true); err == nil {
		t.Fatal("collision accepted")
	}
	if err := c.Relocate(map[string]string{"Trip": ""}, true); err == nil {
		t.Fatal("orphan accepted")
	}
	if err := c.Relocate(map[string]string{"Trip/photo.jpg": ""}, true); err == nil {
		t.Fatal("file deletion accepted")
	}
	afterDry, _ = os.ReadFile(c.path)
	if !reflect.DeepEqual(raw, afterDry) {
		t.Fatal("failed migration changed catalog")
	}
}
