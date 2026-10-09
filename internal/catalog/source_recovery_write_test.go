package catalog

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestAdoptSourceCollectionPreservesTargetAndRejectsConflicts(t *testing.T) {
	c := testCatalog(t)
	ops := []SourceOperation{
		{OperationID: "old-folder", Namespace: "previous", SourceID: "folder", SourceRevision: "f1", Kind: "folder", Title: "Kept folder"},
		{OperationID: "old-album", Namespace: "previous", SourceID: "album", SourceRevision: "a1", Kind: "album", ParentSourceID: "folder", Title: "Kept album"},
	}
	out, err := c.ImportSourceOperations("old-device", ops, false)
	if err != nil || out[0].Status != "applied" || out[1].Status != "applied" {
		t.Fatalf("setup: %+v %v", out, err)
	}
	if err = c.Put(File{Path: "Photos/member.jpg", Present: true}); err != nil {
		t.Fatal(err)
	}
	f, _ := c.Get("Photos/member.jpg")
	album, err := c.ChangeAlbumMembers(out[1].ServerID, 1, []string{f.EntryID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeAlbums, beforeFolders := c.Albums(), append([]CollectionFolder(nil), c.collections.Folders...)
	a := SourceRecoveryAdoption{
		OperationID: "adopt-album", FromDeviceID: "old-device", FromNamespace: "previous", FromSourceID: "album",
		FromSourceRevision: "a1", FromServerRevision: 1, Namespace: "current", SourceID: "same-album",
		ServerID: album.ID, ExpectedServerRevision: album.Revision,
	}
	m, err := c.AdoptSourceCollection("current-device", a)
	if err != nil || m.ServerID != album.ID || m.ServerRevision != album.Revision || m.SourceRevision != "a1" {
		t.Fatal(m, err)
	}
	if !reflect.DeepEqual(beforeAlbums, c.Albums()) || !reflect.DeepEqual(beforeFolders, c.collections.Folders) {
		t.Fatal("adoption changed album membership or hierarchy")
	}
	old, err := c.SourceMapping("old-device", "previous", "album")
	if err != nil || old.ServerRevision != 1 || old.ServerID != album.ID {
		t.Fatal("old mapping overwritten", old, err)
	}
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	actual, err := c.SourceMapping("current-device", "current", "same-album")
	if err != nil || actual != m {
		t.Fatal("adoption did not persist", actual, err)
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if replay, e := c.AdoptSourceCollection("current-device", a); e != nil || replay != m {
		t.Fatal("lost response was not replayable", replay, e)
	}
	after, _ := os.ReadFile(c.path)
	if !bytes.Equal(raw, after) {
		t.Fatal("replay rewrote catalog")
	}
	changed := a
	changed.ServerID = out[0].ServerID
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrSourceConflict) {
		t.Fatal("operation ID accepted a different payload", err)
	}
	changed = a
	changed.OperationID = "different-operation"
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrSourceConflict) {
		t.Fatal("existing destination mapping was overwritten", err)
	}
	changed.SourceID = "different-source"
	changed.ExpectedServerRevision--
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatal("stale current target revision accepted", err)
	}
	changed.ExpectedServerRevision++
	changed.FromServerRevision++
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatal("stale old mapping accepted", err)
	}
	changed.FromServerRevision--
	changed.FromDeviceID = "foreign-device"
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("invented old mapping accepted", err)
	}
	// A folder is adopted by stable ID while its subtree stays intact.
	folder := a
	folder.OperationID, folder.FromSourceID, folder.FromSourceRevision = "adopt-folder", "folder", "f1"
	folder.SourceID, folder.ServerID, folder.ExpectedServerRevision = "same-folder", out[0].ServerID, 1
	if _, err = c.AdoptSourceCollection("current-device", folder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeAlbums, c.Albums()) || !reflect.DeepEqual(beforeFolders, c.collections.Folders) {
		t.Fatal("folder adoption changed album nesting or membership")
	}
	// Subsequent source writes must address the adopted ID and retain the
	// inherited folder and members while advancing the current source revision.
	update := SourceOperation{OperationID: "later-update", Namespace: "current", SourceID: "same-album", SourceRevision: "a2", Kind: "album", ParentSourceID: "same-folder", Title: "Updated", ExpectedRevision: album.Revision}
	updates, err := c.ImportSourceOperations("current-device", []SourceOperation{update}, false)
	if err != nil || updates[0].Status != "applied" || updates[0].ServerID != album.ID || updates[0].Revision != album.Revision+1 {
		t.Fatal("adopted source could not write", updates, err)
	}
	currentAlbum := c.Albums()[0]
	if currentAlbum.ParentID != out[0].ServerID || len(currentAlbum.AssetIDs) != 1 || currentAlbum.AssetIDs[0] != f.EntryID {
		t.Fatal("source update lost nesting or membership", currentAlbum)
	}
	if err = c.DeleteAlbum(album.ID, currentAlbum.Revision); err != nil {
		t.Fatal(err)
	}
	changed = a
	changed.OperationID, changed.SourceID = "deleted-target", "deleted-source"
	if _, err = c.AdoptSourceCollection("current-device", changed); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatal("deleted target resurrected", err)
	}
}

func TestAdoptSourceCollectionRollbackOnSaveError(t *testing.T) {
	c := testCatalog(t)
	out, err := c.ImportSourceOperations("old", []SourceOperation{{OperationID: "old", Namespace: "photokit", SourceID: "album", SourceRevision: "1", Kind: "album", Title: "Keep"}}, false)
	if err != nil || out[0].Status != "applied" {
		t.Fatal(out, err)
	}
	a := SourceRecoveryAdoption{OperationID: "adopt", FromDeviceID: "old", FromNamespace: "photokit", FromSourceID: "album", FromSourceRevision: "1", FromServerRevision: 1, Namespace: "current", SourceID: "album", ServerID: out[0].ServerID, ExpectedServerRevision: 1}
	path := c.path
	c.path = t.TempDir() // Atomic replace cannot write to a directory.
	if _, err = c.AdoptSourceCollection("new", a); err == nil {
		t.Fatal("expected save failure")
	}
	c.path = path
	if _, err = c.SourceMapping("new", "current", "album"); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed save left a mapping", err)
	}
	if _, ok := c.collections.Operations[sourceKey("new", "current", "adopt")]; ok {
		t.Fatal("failed save consumed operation ID")
	}
}
