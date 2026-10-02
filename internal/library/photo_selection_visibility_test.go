package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoSelectionHideGatesAndRestore(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	file, _ := l.catalog.Get("Photos/a.jpg")
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Private", AssetIDs: []string{file.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	selectID := func(hidden bool) string {
		t.Helper()
		selection, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{IDs: []string{file.EntryID}, Filter: PhotoSearchOptions{Hidden: hidden}})
		if err != nil {
			t.Fatal(err)
		}
		return selection.ID
	}
	old := selectID(false)
	if count, err := l.ChangePhotoSelectionVisibility(ctx, old, false, "hide"); err != nil || count != 1 {
		t.Fatalf("hide %d %v", count, err)
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, old, false, "unhide"); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("stale selection accepted: %v", err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID == file.EntryID {
		t.Fatalf("timeline leak %+v %v", page, err)
	}
	search, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "*"})
	if err != nil || len(search.Items) != 1 {
		t.Fatalf("search leak %+v %v", search, err)
	}
	members, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 10, false)
	if err != nil || len(members.AssetIDs) != 0 {
		t.Fatalf("album leak %+v %v", members, err)
	}
	if _, err := l.PhotoDetail(ctx, file.EntryID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("detail leak: %v", err)
	}
	if err := l.ReadPhotoOriginal(ctx, file.EntryID, false, func(PhotoItem, PhotoRangeSource) error { return nil }); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("original leak: %v", err)
	}
	if _, release, err := l.PreparePhotoExport(ctx, []string{file.EntryID}, false); err == nil {
		release()
		t.Fatal("normal grab export allowed hidden asset")
	}
	// Persist through a cold encrypted catalog/index reload.
	stopPhotoIndexSaveForTest(l)
	l.catalog = catalog.New(filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	l.clearSessionCache()
	hidden, err := l.PhotoTimelinePage(ctx, 10, "", "", "hidden", "")
	if err != nil || len(hidden.Items) != 1 || hidden.Items[0].ID != file.EntryID {
		t.Fatalf("hidden reload %+v %v", hidden, err)
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, selectID(true), true, "unhide"); err != nil {
		t.Fatal(err)
	}
	restored, _ := l.catalog.Get(file.Path)
	if restored.EntryID != file.EntryID || restored.Hash != file.Hash || restored.Hidden {
		t.Fatal("restore changed original")
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, selectID(false), false, "set_archived"); err != nil {
		t.Fatal(err)
	}
	archived, err := l.PhotoTimelinePage(ctx, 10, "", "", "archived", "")
	if err != nil || len(archived.Items) != 1 {
		t.Fatalf("archive %+v %v", archived, err)
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, selectID(false), false, "unarchive"); err != nil {
		t.Fatal(err)
	}
}

func TestPhotoSelectionCannotUnhideInheritedFolder(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/Private/a.jpg", []byte("photo")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	file, _ := l.catalog.Get("Photos/Private/a.jpg")
	selection, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{IDs: []string{file.EntryID}, Mode: "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, selection.ID, true, "unhide"); !errors.Is(err, ErrPhotoSearch) {
		t.Fatalf("inherited visibility overridden: %v", err)
	}
}

func TestPhotoSelectionStaleBatchDoesNotHideAnyPhoto(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	ids := []string{}
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
		file, _ := l.catalog.Get(name)
		ids = append(ids, file.EntryID)
	}
	selection, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	caption := "changed"
	if _, err := l.UpdatePhoto(ctx, ids[1], PhotoUpdate{Caption: &caption}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ChangePhotoSelectionVisibility(ctx, selection.ID, false, "hide"); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("stale batch %v", err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("partial mutation %+v %v", page, err)
	}
}

func TestPhotoSelectionHideIncludesPairedMotion(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	parts := []catalog.PhotoIngestFile{}
	for i, name := range []string{"still.jpg", "motion.mov"} {
		from := ".weazl-mobile-pending/pair/" + name
		if _, err := l.Put(ctx, from, []byte(name)); err != nil {
			t.Fatal(err)
		}
		file, _ := l.catalog.Get(from)
		id, kind := "original", "image/jpeg"
		if i == 1 {
			id, kind = "motion", "video/quicktime"
		}
		parts = append(parts, catalog.PhotoIngestFile{ID: id, From: from, To: "Photos/Pair/" + name, Hash: file.Hash, Size: file.Size, MediaType: kind})
	}
	file, err := l.CommitPhotoIngest(ctx, catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "pair", SourceRevision: "1", Files: parts})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"hide", "unhide"} {
		hidden := action == "unhide"
		selection, err := l.CreatePhotoSelection(ctx, PhotoSelectionOptions{IDs: []string{file.EntryID}, Filter: PhotoSearchOptions{Hidden: hidden}})
		if err != nil {
			t.Fatal(err)
		}
		if count, err := l.ChangePhotoSelectionVisibility(ctx, selection.ID, hidden, action); err != nil || count != 1 {
			t.Fatalf("%s %d %v", action, count, err)
		}
		for _, part := range parts {
			current, _ := l.catalog.Get(part.To)
			if current.Hidden == hidden {
				t.Fatalf("component %s did not follow %s", part.To, action)
			}
			if action == "hide" {
				if _, err := l.PhotoDetail(ctx, current.EntryID); !errors.Is(err, catalog.ErrNotFound) {
					t.Fatalf("hidden component leaked %v", err)
				}
			}
		}
	}
}
