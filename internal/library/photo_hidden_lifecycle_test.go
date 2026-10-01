package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoHiddenRootNestedMovesRekeyAndReload(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos", "Photos/Private", "Photos/Private/Child", "Photos/Public"} {
		if err := l.Mkdir(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Photos/Private/Child/still.jpg", "Photos/Private/loose.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	file, _ := l.catalog.Get("Photos/Private/Child/still.jpg")
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Preserved", AssetIDs: []string{file.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	assertCount := func(hidden bool, count int) {
		t.Helper()
		mode := "all"
		if hidden {
			mode = "hidden"
		}
		page, err := l.PhotoTimelinePage(ctx, 10, "", "", mode, "")
		if err != nil || len(page.Items) != count {
			t.Fatalf("%s count=%d want=%d %v", mode, len(page.Items), count, err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos", true); err != nil {
		t.Fatal(err)
	}
	assertCount(false, 0)
	assertCount(true, 2)
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private/Child", true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos", false); err != nil {
		t.Fatal(err)
	}
	assertCount(false, 1)
	assertCount(true, 1)
	if err := l.Rename(ctx, "Photos/Private/Child", "Photos/Public/Renamed"); err != nil {
		t.Fatal(err)
	}
	assertCount(true, 1)
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	assertCount(false, 0)
	assertCount(true, 2)
	if err := l.Rename(ctx, "Photos/Private/loose.jpg", "Photos/Public/loose.jpg"); err != nil {
		t.Fatal(err)
	}
	assertCount(false, 1)
	assertCount(true, 1)
	if _, err := l.Put(ctx, "Photos/Public/Renamed/new.jpg", []byte("new hidden image")); err != nil {
		t.Fatal(err)
	}
	assertCount(true, 2)
	// Catalog encryption survives vault rekey; cached private state is rebuilt.
	stopPhotoIndexSaveForTest(l)
	if err := l.vault.Rekey([]byte("photo-page-test"), []byte("next-photo-pass"), []byte("next-photo-pass")); err != nil {
		t.Fatal(err)
	}
	l.clearSessionCache()
	assertCount(false, 1)
	assertCount(true, 2)
	l.catalog = catalog.New(filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	l.clearSessionCache()
	assertCount(false, 1)
	assertCount(true, 2)
	members, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 10, true)
	if err != nil || len(members.AssetIDs) != 1 || members.AssetIDs[0] != file.EntryID {
		t.Fatalf("membership=%+v %v", members, err)
	}
	if _, err := l.PhotoDetail(ctx, file.EntryID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("hidden detail leaked after reload")
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Public/Renamed", false); err != nil {
		t.Fatal(err)
	}
	assertCount(false, 3)
	assertCount(true, 0)
	body, err := l.Get(ctx, "Photos/Public/Renamed/still.jpg")
	if err != nil || string(body) != file.Path {
		t.Fatalf("original changed: %q %v", body, err)
	}
}

func TestPhotoHiddenImplicitMobileFolderGetsStableIdentity(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.Put(ctx, "Photos/Mobile/phone/asset/1/still.jpg", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PhotoPage(ctx, 10, "", ""); err != nil {
		t.Fatal(err)
	}
	folder, err := l.SetPhotoFolderHidden(ctx, "Photos/Mobile/phone", true)
	if err != nil || folder.EntryID == "" {
		t.Fatalf("implicit folder visibility=%+v %v", folder, err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatal("implicit hidden folder leaked")
	}
	l.clearSessionCache()
	page, err = l.PhotoTimelinePage(ctx, 10, "", "", "hidden", "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal("implicit visibility did not reload")
	}
	visible, err := l.SetPhotoFolderHidden(ctx, "Photos/Mobile/phone", false)
	if err != nil || visible.EntryID != folder.EntryID {
		t.Fatal("unhide changed folder identity")
	}
}
