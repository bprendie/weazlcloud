package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestSourceRecoveryPreservesCollectionsAndMemberships(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()
	ops := []SourceOperation{
		{OperationID: "folder", Namespace: "old-photokit", SourceID: "folder/id", SourceRevision: "source-f1", Kind: "folder", Title: "Folder"},
		{OperationID: "album", Namespace: "old-photokit", SourceID: "album/id", SourceRevision: "source-a1", Kind: "album", Title: "Album", ParentSourceID: "folder/id"},
	}
	out, err := c.ImportSourceOperations("old-device", ops, false)
	if err != nil || out[0].Status != "applied" || out[1].Status != "applied" {
		t.Fatalf("create: %+v %v", out, err)
	}
	if err = c.Put(File{Path: "Photos/photo.jpg", Present: true}); err != nil {
		t.Fatal(err)
	}
	f, _ := c.Get("Photos/photo.jpg")
	a, err := c.ChangeAlbumMembers(out[1].ServerID, 1, []string{f.EntryID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Same source ID on a different current device/namespace must be explicit,
	// not silently selected over the old mapping with existing memberships.
	other := ops[1]
	other.Namespace, other.ParentSourceID = "new-photokit", ""
	if _, err = c.ImportSourceOperations("current-device", []SourceOperation{other}, false); err != nil {
		t.Fatal(err)
	}
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(c.path)
	state, albums, journal := cloneCollectionState(c.collections), c.Albums(), c.journal.SyncPosition
	page, err := c.LookupSourceCollections(ctx, []string{"folder/id", "album/id", "unknown"}, "", "", 200)
	if err != nil || len(page.Matches) != 3 || page.HasMore {
		t.Fatalf("lookup: %+v %v", page, err)
	}
	for _, m := range page.Matches {
		if !m.TargetExists || m.CurrentServerRevision == nil {
			t.Fatal(m)
		}
		if m.ServerID == a.ID && (m.SourceRevision != "source-a1" || m.ServerRevision != 1 || *m.CurrentServerRevision != a.Revision) {
			t.Fatal("stale mapping revision confused with current revision", m)
		}
	}
	after, _ := os.ReadFile(c.path)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(state, c.collections) || !reflect.DeepEqual(albums, c.Albums()) || journal != c.journal.SyncPosition {
		t.Fatal("recovery changed catalog, tree, memberships, mappings, receipts or journal")
	}
	if err = c.DeleteAlbum(a.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteCollectionFolder(out[0].ServerID, 1); err != nil {
		t.Fatal(err)
	}
	page, err = c.LookupSourceCollections(ctx, []string{"album/id", "folder/id"}, "old-photokit", "", 200)
	if err != nil || len(page.Matches) != 2 {
		t.Fatal(page, err)
	}
	for _, m := range page.Matches {
		if m.TargetExists || m.CurrentServerRevision != nil || m.ServerID == "" || m.ServerRevision != 1 {
			t.Fatal("deleted target mapping was lost or resurrected", m)
		}
	}
}

func TestSourceRecoveryBoundedPagingAndAssetExclusion(t *testing.T) {
	c := testCatalog(t)
	c.collections.Sources = map[string]SourceMapping{}
	for i := 0; i < 451; i++ {
		device := fmt.Sprintf("device-%d", i)
		c.collections.Sources[sourceKey(device, "photokit", "same")] = SourceMapping{DeviceID: device, Namespace: "photokit", SourceID: "same", Kind: "album", ServerID: "deleted", ServerRevision: 7}
	}
	c.collections.Sources["asset"] = SourceMapping{SourceID: "same", Kind: "asset"}
	seen, after := map[string]bool{}, ""
	for {
		page, err := c.LookupSourceCollections(context.Background(), []string{"same"}, "", after, 200)
		if err != nil || len(page.Matches) > 200 || len(page.Matches) == 0 {
			t.Fatal(page, err)
		}
		for _, m := range page.Matches {
			if seen[m.DeviceID] || m.Kind != "album" {
				t.Fatal("duplicate or asset in recovery page", m)
			}
			seen[m.DeviceID] = true
		}
		if !page.HasMore {
			break
		}
		if page.After <= after {
			t.Fatal("cursor failed to advance")
		}
		after = page.After
	}
	if len(seen) != 451 {
		t.Fatal("truncated matches", len(seen))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.LookupSourceCollections(ctx, []string{"same"}, "", "", 200); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, ids := range [][]string{nil, {"same", "same"}, {""}, {"bad\x00id"}} {
		if _, err := c.LookupSourceCollections(context.Background(), ids, "", "", 200); !errors.Is(err, ErrSourceRecovery) {
			t.Fatal("accepted invalid input", ids, err)
		}
	}
}
