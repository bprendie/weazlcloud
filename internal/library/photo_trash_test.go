package library

import (
	"context"
	"errors"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoTrashRestoresFolderChildrenAndPreservesHiddenState(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/Private", "Photos/Public"} {
		if err := l.Mkdir(ctx, name); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Put(ctx, name+"/child.jpg", []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	before, err := l.PhotoTimelinePage(ctx, 20, "", "", "hidden", "")
	if err != nil || len(before.Items) != 1 {
		t.Fatalf("before=%+v error=%v", before, err)
	}
	childID := before.Items[0].ID
	if err := l.Delete("Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete("Photos/Public"); err != nil {
		t.Fatal(err)
	}
	normal, err := l.PhotoTrash(ctx, false)
	if err != nil || len(normal) != 2 {
		t.Fatalf("normal trash=%+v error=%v", normal, err)
	}
	hidden, err := l.PhotoTrash(ctx, true)
	if err != nil || len(hidden) != 2 {
		t.Fatalf("hidden trash=%+v error=%v", hidden, err)
	}
	if err := l.RestorePhoto(ctx, childID, false); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("hidden restore exposed: %v", err)
	}
	if err := l.RestorePhoto(ctx, childID, true); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("child restored without hidden parent: %v", err)
	}
	for _, file := range hidden {
		if file.Folder {
			if err := l.RestorePhoto(ctx, file.EntryID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, file := range normal {
		if file.Folder {
			if err := l.RestorePhoto(ctx, file.EntryID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	after, err := l.PhotoTimelinePage(ctx, 20, "", "", "hidden", "")
	if err != nil || len(after.Items) != 1 || after.Items[0].ID != childID {
		t.Fatalf("restored hidden timeline=%+v error=%v", after, err)
	}
	visible, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(visible.Items) != 1 || visible.Items[0].Path != "Photos/Public/child.jpg" {
		t.Fatalf("restored visible timeline=%+v error=%v", visible, err)
	}
	if err := l.Copy(ctx, "Photos/Public", "Photos/Copied"); err != nil {
		t.Fatal(err)
	}
	copied, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(copied.Items) != 2 {
		t.Fatalf("copied timeline=%+v error=%v", copied, err)
	}
}
