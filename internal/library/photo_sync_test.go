package library

import (
	"context"
	"errors"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoSyncSnapshotReconcilesConcurrentChangesAndDeletes(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := l.catalog.Get("Photos/a.jpg")
	first, err := l.PhotoSync(ctx, "", 1, false)
	if err != nil || first.NextCursor == "" || len(first.Items) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	caption := "changed during initial sync"
	if _, err := l.UpdatePhoto(ctx, a.EntryID, PhotoUpdate{Caption: &caption}, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete("Photos/b.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/late.jpg", []byte("late")); err != nil {
		t.Fatal(err)
	}
	seen := map[string]PhotoItem{first.Items[0].ID: first.Items[0]}
	cursor := first.NextCursor
	var checkpoint string
	for i := 0; i < 10; i++ {
		page, err := l.PhotoSync(ctx, cursor, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			seen[item.ID] = item
		}
		if !page.HasMore {
			checkpoint = page.Checkpoint
			break
		}
		cursor = page.NextCursor
	}
	if checkpoint == "" {
		t.Fatal("initial sync did not complete")
	}
	for i := 0; i < 10; i++ {
		page, err := l.PhotoSync(ctx, checkpoint, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range page.Changes {
			if change.Deleted {
				delete(seen, change.ID)
			} else if change.Item != nil {
				seen[change.ID] = *change.Item
			}
		}
		if !page.HasMore {
			checkpoint = page.Checkpoint
			break
		}
		checkpoint = page.NextCursor
	}
	if len(seen) != 2 || seen[a.EntryID].Caption != caption {
		t.Fatalf("client did not reconcile=%+v", seen)
	}
	if err := l.AcknowledgePhotoSync(ctx, "phone", checkpoint, false); err != nil {
		t.Fatal(err)
	}
	if resumed, err := l.PhotoDeviceCheckpoint(ctx, "phone", false); err != nil || resumed == "" {
		t.Fatalf("resume=%q err=%v", resumed, err)
	}
	other := newPhotoIndexTestLibrary(t)
	if _, err := other.PhotoSync(ctx, checkpoint, 10, false); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("cross-owner checkpoint accepted: %v", err)
	}
}

func TestPhotoSyncHiddenContextIsBoundAndNormalSnapshotExcludesHidden(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/Private/secret.jpg", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoSync(ctx, "", 200, false)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("hidden snapshot leak=%+v err=%v", page, err)
	}
	if _, err := l.PhotoSync(ctx, page.Checkpoint, 200, true); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("changed context accepted: %v", err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", false); err != nil {
		t.Fatal(err)
	}
	delta, err := l.PhotoSync(ctx, page.Checkpoint, 200, false)
	if err != nil || !delta.ResyncRequired {
		t.Fatalf("unhide did not require reconciliation=%+v err=%v", delta, err)
	}
}
