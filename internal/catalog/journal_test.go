package catalog

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestEncryptedJournalCheckpointDetectsRestoreAndBranch(t *testing.T) {
	c := testCatalog(t)
	initial, err := c.SyncPosition()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(File{Path: "Photos/private-sentinel.jpg", Size: 4, Present: true}); err != nil {
		t.Fatal(err)
	}
	records, landed, more, err := c.ChangesAfter(initial, 100)
	if err != nil || more || len(records) != 1 || !records[0].Photos {
		t.Fatalf("journal=%+v more=%v err=%v", records, more, err)
	}
	if err := c.AcknowledgeDevice("phone", landed); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(backup), "private-sentinel") || strings.Contains(string(backup), "phone") {
		t.Fatal("private journal/checkpoint leaked in plaintext")
	}
	reopened := New(c.path, c.vault)
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	if position, err := reopened.DeviceCheckpoint("phone"); err != nil || position != landed {
		t.Fatalf("durable checkpoint=%+v err=%v", position, err)
	}
	if err := reopened.Put(File{Path: "Photos/second.jpg", Size: 4, Present: true}); err != nil {
		t.Fatal(err)
	}
	later, err := reopened.SyncPosition()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path, backup, 0600); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ValidateSync(later); !errors.Is(err, ErrSyncExpired) {
		t.Fatalf("restored checkpoint accepted: %v", err)
	}
	if err := reopened.Put(File{Path: "Photos/branched.jpg", Size: 4, Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ValidateSync(later); !errors.Is(err, ErrSyncExpired) {
		t.Fatalf("same-sequence branch accepted: %v", err)
	}
	if err := reopened.ValidateSync(landed); err != nil {
		t.Fatalf("retained common checkpoint rejected: %v", err)
	}
}

func TestJournalIncludesDeletionAndAlbumChanges(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/a.jpg", Size: 4, Present: true}); err != nil {
		t.Fatal(err)
	}
	photo, _ := c.Get("Photos/a.jpg")
	position, err := c.SyncPosition()
	if err != nil {
		t.Fatal(err)
	}
	album, err := c.SaveAlbum(Album{Title: "Shared trip", AssetIDs: []string{photo.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("Photos/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAlbum(album.ID, album.Revision); err != nil {
		t.Fatal(err)
	}
	records, _, _, err := c.ChangesAfter(position, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Kind != "album" || !records[1].Deleted || !records[2].Deleted || records[1].ID != photo.EntryID || records[2].ID != album.ID {
		t.Fatalf("changes=%+v", records)
	}
}
