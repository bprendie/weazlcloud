package library

import (
	"context"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoSearchWildcardsCaptionsDateFavoriteAndHidden(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if err := l.Mkdir(ctx, "Photos/Trip"); err != nil {
		t.Fatal(err)
	}
	day, _ := time.Parse(time.RFC3339, "2024-05-12T10:00:00-04:00")
	visible, err := l.Put(ctx, "Photos/Trip/Summer-beach.jpg", []byte("visible"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := l.Put(ctx, "Photos/Private/Summer-beach.jpg", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	visible, _ = l.catalog.Get(visible.Path)
	secret, _ = l.catalog.Get(secret.Path)
	for _, item := range []struct {
		id     string
		hidden bool
	}{{visible.EntryID, false}, {secret.EntryID, false}} {
		caption := "lake day"
		favorite := item.id == visible.EntryID
		if _, err := l.UpdatePhoto(ctx, item.id, PhotoUpdate{Favorite: &favorite, Caption: &caption, Capture: &catalog.CaptureMetadata{Time: &day, Source: "user", UserCorrected: true}}, item.hidden); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "summer-*", Type: "image", DateFrom: "2024-05-12", DateTo: "2024-05-12", Favorite: true, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != visible.EntryID {
		t.Fatalf("visible search=%+v err=%v", page, err)
	}
	if _, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "lake day"}); err != nil {
		t.Fatal(err)
	}
	hidden, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "summer-*", Hidden: true})
	if err != nil || len(hidden.Items) != 1 || hidden.Items[0].ID != secret.EntryID {
		t.Fatalf("hidden search=%+v err=%v", hidden, err)
	}
	if _, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{DateFrom: "2024-02-30"}); err == nil {
		t.Fatal("invalid capture-date search accepted")
	}
	archived := true
	if _, err := l.UpdatePhoto(ctx, visible.EntryID, PhotoUpdate{Archived: &archived}, false); err != nil {
		t.Fatal(err)
	}
	timeline, err := l.PhotoTimelinePage(ctx, 10, "", "", "all", "")
	if err != nil || len(timeline.Items) != 0 {
		t.Fatalf("archived photo remained in timeline: %+v err=%v", timeline, err)
	}
	archive, err := l.PhotoTimelinePage(ctx, 10, "", "", "archived", "")
	if err != nil || len(archive.Items) != 1 || !archive.Items[0].Archived {
		t.Fatalf("archive view=%+v err=%v", archive, err)
	}
}

func TestCustomPhotoAlbumMembershipSurvivesHiddenFiltering(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	visible, err := l.Put(ctx, "Photos/visible.jpg", []byte("visible"))
	if err != nil {
		t.Fatal(err)
	}
	private, err := l.Put(ctx, "Photos/Private/secret.jpg", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	visible, _ = l.catalog.Get(visible.Path)
	private, _ = l.catalog.Get(private.Path)
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Weekend", AssetIDs: []string{visible.EntryID, private.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoTimelinePage(ctx, 10, "", "album:"+album.ID, "all", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("album timeline=%+v err=%v", page, err)
	}
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoTimelinePage(ctx, 10, "", "album:"+album.ID, "all", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != visible.EntryID {
		t.Fatalf("hidden member leaked into album=%+v err=%v", page, err)
	}
	for _, view := range []struct {
		hidden bool
		id     string
	}{{false, visible.EntryID}, {true, private.EntryID}} {
		header, err := l.PhotoAlbumHeader(ctx, album.ID, view.hidden)
		if err != nil || header.Count != 1 || len(header.AssetIDs) != 1 || header.AssetIDs[0] != view.id {
			t.Fatalf("album mutation header leaked context: %+v %v", header, err)
		}
		albums, err := l.PhotoAlbums(ctx, view.hidden)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, header := range albums {
			if header.ID == album.ID {
				found = true
				if header.Count != 1 || len(header.AssetIDs) != 1 || header.AssetIDs[0] != view.id {
					t.Fatalf("album list leaked context: %+v", header)
				}
			}
		}
		if !found {
			t.Fatal("custom album missing")
		}
	}
	l.ForgetVaultSession()
	if l.albumMetadata != nil {
		t.Fatal("private album metadata survived forgotten session")
	}
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/Private", false); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoTimelinePage(ctx, 10, "", "album:"+album.ID, "all", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("unhidden member or membership was lost=%+v err=%v", page, err)
	}
	if err = l.DeletePhotoAlbum(ctx, album.ID, album.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Metadata(ctx, "Photos/visible.jpg"); err != nil {
		t.Fatalf("deleting album removed original: %v", err)
	}
}
