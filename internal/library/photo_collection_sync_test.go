package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"testing"
)

func TestCollectionSnapshotsAndDeltasAreBoundedAndMediaNeutral(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	folder, err := l.SavePhotoCollection(ctx, catalog.CollectionFolder{Title: "Virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Mkdir(ctx, "Photos/private"); err != nil {
		t.Fatal(err)
	}
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/private", true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Photos/private/a.jpg", "Photos/b.jpg"} {
		if err = l.catalog.Put(catalog.File{Path: name, Size: 1, Hash: name, Snap: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	l.buildPhotoIndex()
	private, _ := l.catalog.Get("Photos/private/a.jpg")
	visible, _ := l.catalog.Get("Photos/b.jpg")
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Mixed", ParentID: folder.ID, ParentSet: true, AssetIDs: []string{private.EntryID, visible.EntryID}, CoverID: private.EntryID})
	if err != nil {
		t.Fatal(err)
	}
	var base catalog.SyncPosition
	after := ""
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		var position *catalog.SyncPosition
		if after != "" {
			position = &base
		}
		page, err := l.PhotoCollections(ctx, after, position, 1)
		if err != nil || page.Schema != 2 || len(page.Nodes) > 1 {
			t.Fatalf("snapshot: %+v %v", page, err)
		}
		if i == 0 {
			base = page.Position
		} else if page.Position != base {
			t.Fatal("snapshot changed baseline")
		}
		for _, node := range page.Nodes {
			if seen[node.ID] {
				t.Fatal("snapshot repeated node")
			}
			seen[node.ID] = true
			if node.Album != nil && (node.Album.CoverID != "" || len(node.Album.AssetIDs) != 0 || node.Album.ParentID != folder.ID) {
				t.Fatal("snapshot media leak/parent loss")
			}
		}
		if !page.HasMore {
			break
		}
		after = page.Next
	}
	if len(seen) != 2 {
		t.Fatal("snapshot missed nodes")
	}
	normal, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 200, false)
	if err != nil || len(normal.AssetIDs) != 1 || normal.AssetIDs[0] != visible.EntryID {
		t.Fatalf("visible: %+v %v", normal, err)
	}
	hidden, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 200, true)
	if err != nil || len(hidden.AssetIDs) != 1 || hidden.AssetIDs[0] != private.EntryID {
		t.Fatalf("hidden: %+v %v", hidden, err)
	}
	album.Description = "Edited"
	if _, err = l.SavePhotoAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"One", "Two", "Three"} {
		if _, err = l.SavePhotoCollection(ctx, catalog.CollectionFolder{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	// Unrelated asset records must advance the cursor without leaking media.
	if err = l.catalog.Put(catalog.File{Path: "Photos/private/new.jpg", Size: 1, Hash: "new", Snap: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.PhotoCollections(ctx, after, &base, 1); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatal("changed snapshot accepted")
	}
	position := base
	count := 0
	for i := 0; i < 20; i++ {
		page, err := l.PhotoCollectionChanges(ctx, position, 1)
		if err != nil || page.Schema != 2 || len(page.Changes) > 1 {
			t.Fatalf("delta: %+v %v", page, err)
		}
		for _, change := range page.Changes {
			count++
			if change.File != nil || change.Album != nil && (change.Album.CoverID != "" || len(change.Album.AssetIDs) != 0) {
				t.Fatal("delta media leak")
			}
		}
		if page.HasMore && page.Position.Sequence <= position.Sequence {
			t.Fatal("delta stalled")
		}
		position = page.Position
		if !page.HasMore {
			break
		}
	}
	if count != 4 {
		t.Fatalf("lost changes: %d", count)
	}
	// Restore creates an incompatible future checkpoint; it is never replayed.
	position.Sequence++
	if _, err = l.PhotoCollectionChanges(ctx, position, 1); !errors.Is(err, catalog.ErrSyncExpired) {
		t.Fatal("future checkpoint accepted")
	}
}
