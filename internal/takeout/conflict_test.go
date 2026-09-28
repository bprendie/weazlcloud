package takeout

import (
	"context"
	"testing"
)

func TestFileFolderConflictsPreserveTreesAndResume(t *testing.T) {
	lib := parallelLibrary(t)
	z := orderedZIP(t, zipEntry{"a", "original file"}, zipEntry{"a/", ""}, zipEntry{"a/child", "child content"}, zipEntry{"b/child", "other child"}, zipEntry{"b", "incoming file"}, zipEntry{"last", "after collisions"})
	s, err := Import(context.Background(), lib, "trees.zip", z, "", nil, nil)
	if err != nil || s.Imported != 5 || len(s.Renamed) != 1 || len(s.Directories) != 1 || s.ProcessedBytes != s.Bytes {
		t.Fatalf("tree import: %+v %v", s, err)
	}
	for name, want := range map[string]string{"a": "original file", s.Directories[0].Path + "/child": "child content", "b/child": "other child", s.Renamed[0].Path: "incoming file", "last": "after collisions"} {
		got, err := lib.Get(context.Background(), name)
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	s, err = Import(context.Background(), lib, "trees.zip", z, "", nil, nil)
	if err != nil || s.Imported != 0 || s.Skipped != 5 || len(s.Renamed) != 1 || len(s.Directories) != 1 {
		t.Fatalf("tree resume: %+v %v", s, err)
	}
}

func TestExactDuplicateZIPNamesWithDifferentContent(t *testing.T) {
	lib := parallelLibrary(t)
	z := orderedZIP(t, zipEntry{"same", "first"}, zipEntry{"same", "other"}, zipEntry{"same", "other"})
	s, err := Import(context.Background(), lib, "duplicates.zip", z, "", nil, nil)
	if err != nil || s.Imported != 2 || s.Skipped != 1 || len(s.Renamed) != 2 || s.Renamed[0].Path != s.Renamed[1].Path {
		t.Fatalf("duplicates: %+v %v", s, err)
	}
}
