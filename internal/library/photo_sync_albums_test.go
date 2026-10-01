package library

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoSyncBoundsAlbumHeadersAndMemberships(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 7; i++ {
		file, err := l.Put(ctx, fmt.Sprintf("Photos/%d.jpg", i), []byte("image"))
		if err != nil {
			t.Fatal(err)
		}
		stored, ok := l.catalog.Get(file.Path)
		if !ok {
			t.Fatal("stored photo missing")
		}
		ids = append(ids, stored.EntryID)
	}
	var album catalog.Album
	for i := 0; i < 3; i++ {
		var err error
		album, err = l.SavePhotoAlbum(ctx, catalog.Album{Title: fmt.Sprintf("Album %d", i), AssetIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
	}
	cursor, checkpoint := "", ""
	albums := 0
	for i := 0; i < 20; i++ {
		page, err := l.PhotoSync(ctx, cursor, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items)+len(page.Albums) > 2 {
			t.Fatal("unbounded sync page")
		}
		for _, header := range page.Albums {
			if len(header.AssetIDs) != 0 || header.Count != 7 {
				t.Fatalf("header=%+v", header)
			}
			albums++
		}
		if !page.HasMore {
			checkpoint = page.Checkpoint
			break
		}
		cursor = page.NextCursor
	}
	if checkpoint == "" || albums != 3 {
		t.Fatalf("sync incomplete: albums=%d", albums)
	}
	first, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 2, false)
	if err != nil || len(first.AssetIDs) != 2 || !first.HasMore {
		t.Fatalf("members=%+v %v", first, err)
	}
	if _, err := l.PhotoAlbumMemberships(ctx, album.ID, first.NextCursor, 2, true); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("hidden cursor reused: %v", err)
	}
	album, err = l.ChangePhotoAlbumMembers(ctx, album.ID, album.Revision, nil, ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.PhotoAlbumMemberships(ctx, album.ID, first.NextCursor, 2, false); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("changed membership cursor reused: %v", err)
	}
	delta, err := l.PhotoSync(ctx, checkpoint, 2, false)
	if err != nil || len(delta.Changes) != 1 || !delta.Changes[0].MembershipChanged || len(delta.Changes[0].Album.AssetIDs) != 0 || delta.Changes[0].Album.Count != 6 {
		t.Fatalf("delta=%+v %v", delta, err)
	}
	seen := map[string]bool{}
	cursor = ""
	for i := 0; i < 10; i++ {
		page, err := l.PhotoAlbumMemberships(ctx, album.ID, cursor, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range page.AssetIDs {
			if seen[id] {
				t.Fatal("duplicate member")
			}
			seen[id] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 6 || seen[ids[0]] {
		t.Fatalf("members did not reconcile: %+v", seen)
	}
}
