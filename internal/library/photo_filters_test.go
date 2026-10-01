package library

import (
	"context"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoSearchCameraAndOutsideAlbums(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/root.jpg", "Photos/Photos from 2020/year.jpg", "Photos/Trip/album.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
		file, _ := l.catalog.Get(name)
		updated, err := l.catalog.UpdatePhotoMetadata(file.EntryID, file.Revision, nil, &catalog.MediaMetadata{Camera: "SONY A7IV", Width: 20, Height: 10, Orientation: 1})
		if err != nil {
			t.Fatal(err)
		}
		l.publishChange(Change{Kind: "photo-metadata", Paths: []string{updated.Path}})
	}
	page, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Camera: "sony", OutsideAlbums: true, Limit: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("outside search=%+v error=%v", page, err)
	}
	file, _ := l.catalog.Get("Photos/root.jpg")
	if _, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Custom", AssetIDs: []string{file.EntryID}}); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoSearchPage(ctx, PhotoSearchOptions{Camera: "sony", OutsideAlbums: true, Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Path != "Photos/Photos from 2020/year.jpg" {
		t.Fatalf("album edit did not invalidate outside cache: %+v error=%v", page, err)
	}
	page, err = l.PhotoSearchPage(ctx, PhotoSearchOptions{Camera: "Canon", Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("camera filter=%+v error=%v", page, err)
	}
}
