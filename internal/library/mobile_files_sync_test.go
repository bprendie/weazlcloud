package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestMobileFilesSyncBaselineTombstonesAndCheckpoint(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Docs/a", "Docs/b", "outside"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := l.catalog.Get("Docs/a")
	b, _ := l.catalog.Get("Docs/b")
	first, err := l.FilesSync(ctx, "phone", "", "Docs", 1)
	if err != nil || !first.HasMore || len(first.Items) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err := l.catalog.Rename("Docs/a", "Elsewhere/a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete("Docs/b"); err != nil {
		t.Fatal(err)
	}
	late, err := l.Put(ctx, "Docs/late", []byte("late"))
	if err != nil {
		t.Fatal(err)
	}
	late, _ = l.catalog.Get("Docs/late")
	seen := map[string]MobileFileItem{first.Items[0].ID: first.Items[0]}
	cursor := first.NextCursor
	for i := 0; i < 10; i++ {
		page, err := l.FilesSync(ctx, "phone", cursor, "Docs", 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			seen[item.ID] = item
		}
		if !page.HasMore {
			cursor = page.Checkpoint
			break
		}
		cursor = page.NextCursor
	}
	for i := 0; i < 10; i++ {
		page, err := l.FilesSync(ctx, "phone", cursor, "Docs", 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range page.Changes {
			if change.Deleted {
				delete(seen, change.ID)
			} else {
				seen[change.ID] = *change.Item
			}
		}
		if !page.HasMore {
			cursor = page.Checkpoint
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 1 || seen[late.EntryID].Path != "Docs/late" || seen[a.EntryID].ID != "" || seen[b.EntryID].ID != "" {
		t.Fatalf("unreconciled manifest=%v", seen)
	}
	if err := l.AcknowledgeFilesSync(ctx, "phone", cursor, "Docs"); err != nil {
		t.Fatal(err)
	}
	if raw, err := l.FilesDeviceCheckpoint(ctx, "phone", "Docs"); err != nil || raw == "" {
		t.Fatalf("resume=%q err=%v", raw, err)
	}
	for _, change := range []struct{ device, prefix string }{{"other", "Docs"}, {"phone", "outside"}} {
		if _, err := l.FilesSync(ctx, change.device, cursor, change.prefix, 1); !errors.Is(err, catalog.ErrSyncExpired) {
			t.Fatalf("binding accepted: %v", err)
		}
	}
	if err := l.AcknowledgeFilesSync(ctx, "phone", first.NextCursor, "Docs"); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("snapshot acknowledged: %v", err)
	}
}

func TestMobileFilesSyncRejectsExpiredSessionAndRestoredJournal(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	empty, err := l.FilesSync(ctx, "phone", "", "", 200)
	if err != nil || empty.HasMore || len(empty.Items) != 0 || empty.Checkpoint == "" {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	cursor, err := l.decodeFilesCursor(empty.Checkpoint, "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	cursor.Expires = time.Now().Add(-time.Hour).Unix()
	expired, _ := l.encodeFilesCursor(cursor)
	if _, err := l.FilesSync(ctx, "phone", expired, "", 200); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("expired=%v", err)
	}
	cursor.Expires = time.Now().Add(time.Hour).Unix()
	cursor.Session++
	wrongSession, _ := l.encodeFilesCursor(cursor)
	if _, err := l.FilesSync(ctx, "phone", wrongSession, "", 200); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("session=%v", err)
	}
	cursor.Session--
	cursor.Boot = "previous server process"
	previousBoot, _ := l.encodeFilesCursor(cursor)
	if _, err := l.FilesSync(ctx, "phone", previousBoot, "", 200); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("boot=%v", err)
	}
	cursor.Boot = mobileFilesBoot
	cursor.Position.Hash = "branched or expired journal hash"
	branched, _ := l.encodeFilesCursor(cursor)
	if _, err := l.FilesSync(ctx, "phone", branched, "", 200); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("branch=%v", err)
	}
	path := filepath.Join(filepath.Dir(l.repo), "catalog.enc")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "later", []byte("later")); err != nil {
		t.Fatal(err)
	}
	page, err := l.FilesSync(ctx, "phone", empty.Checkpoint, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.FilesSync(ctx, "phone", page.Checkpoint, "", 200); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatalf("restored checkpoint=%v", err)
	}
}
