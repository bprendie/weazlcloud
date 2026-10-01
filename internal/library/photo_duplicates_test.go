package library

import (
	"context"
	"errors"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoDuplicatesPreferWithoutDeletingOrChangingAlbumMembership(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg", "Photos/Private/hidden.jpg"} {
		if _, err := l.Put(ctx, name, []byte("same content")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	a, _ := l.catalog.Get("Photos/a.jpg")
	b, _ := l.catalog.Get("Photos/b.jpg")
	secret, _ := l.catalog.Get("Photos/Private/hidden.jpg")
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Trip", AssetIDs: []string{a.EntryID, b.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoDuplicates(ctx, "", 20, false)
	if err != nil || len(page.Groups) != 1 || page.Groups[0].Count != 2 {
		t.Fatalf("duplicates=%+v error=%v", page, err)
	}
	if page.Groups[0].ID == a.Hash {
		t.Fatal("global content hash exposed as duplicate identity")
	}
	if err := l.PreferPhoto(ctx, secret.EntryID, false); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("hidden preferred edit=%v", err)
	}
	if err := l.PreferPhoto(ctx, b.EntryID, false); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoDuplicates(ctx, "", 20, false)
	if err != nil || page.Groups[0].Items[0].ID != b.EntryID || !page.Groups[0].Items[0].PreferredPhoto {
		t.Fatalf("preferred=%+v error=%v", page, err)
	}
	if err := l.PreferPhoto(ctx, a.EntryID, false); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoDuplicates(ctx, "", 20, false)
	if err != nil || page.Groups[0].Items[0].ID != a.EntryID || page.Groups[0].Items[1].PreferredPhoto {
		t.Fatalf("preference switch=%+v error=%v", page, err)
	}
	if len(l.catalog.List()) != 4 {
		t.Fatal("preference deleted an original")
	}
	albums := l.catalog.Albums()
	if len(albums) != 1 || albums[0].ID != album.ID || len(albums[0].AssetIDs) != 2 {
		t.Fatalf("membership changed=%+v", albums)
	}
}
