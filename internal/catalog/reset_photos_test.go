package catalog

import (
	"errors"
	"reflect"
	"testing"
)

func TestResetPhotosPreservesDriveAndExpiresDeviceState(t *testing.T) {
	c := testCatalog(t)
	for _, name := range []string{"Photos/private/a.jpg", "Photos/live.mov", "Photos/trash.jpg", ".weazl-mobile-pending/upload/a.heic", "Photos-backup/keep.jpg", "Drive/keep.iso", "Drive/trash.txt"} {
		if err := c.Put(File{Path: name, Size: 10, Hash: name, Snap: "shared-snapshot", Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Photos/trash.jpg", "Drive/trash.txt"} {
		if err := c.Delete(name); err != nil {
			t.Fatal(err)
		}
	}
	c.collections = collectionState{Folders: []CollectionFolder{{ID: "folder", Title: "iCloud", Revision: 1}}, Sources: map[string]SourceMapping{"old": {Kind: "asset", ServerID: "old"}}, Operations: map[string]SourceReceipt{"old": {ServerID: "old"}}}
	c.albums = []Album{{ID: "album", Title: "Trip", Revision: 1}}
	if err := c.saveLocked(); err != nil {
		t.Fatal(err)
	}
	before, _ := c.SyncPosition()
	c.checkpoints = map[string]SyncPosition{"device": before}
	var kept []File
	for _, f := range c.All() {
		if !PhotoResetPath(f.Path) {
			kept = append(kept, f)
		}
	}
	removed, err := c.ResetPhotos()
	if err != nil || len(removed) != 4 {
		t.Fatalf("removed=%d err=%v", len(removed), err)
	}
	if err := c.LoadReadOnly(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.All(), kept) {
		t.Fatal("Drive files or Drive trash changed")
	}
	if len(c.albums)+len(c.collections.Folders)+len(c.collections.Sources)+len(c.collections.Operations)+len(c.checkpoints)+len(c.journal.Records) != 0 {
		t.Fatal("old Photos state survived")
	}
	if err := c.ValidateSync(before); !errors.Is(err, ErrSyncExpired) {
		t.Fatalf("old checkpoint accepted: %v", err)
	}
}

func TestResetPhotosWriteFailurePreservesCatalog(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/a.jpg", Size: 1, Present: true}); err != nil {
		t.Fatal(err)
	}
	before := c.All()
	c.vault.Lock()
	if _, err := c.ResetPhotos(); err == nil {
		t.Fatal("reset without key succeeded")
	}
	if !reflect.DeepEqual(c.All(), before) {
		t.Fatal("failed reset changed memory")
	}
}
