package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
	"path/filepath"
	"testing"
	"time"
)

func newPhotoIndexTestLibrary(t testing.TB) *Library {
	t.Helper()
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("photo-page-test"), []byte("photo-page-test")); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	l.backend = &isolatedLegacy{root: filepath.Join(root, "library")}
	l.photoAutoDisabled = true
	t.Cleanup(func() {
		stopPhotoIndexSaveForTest(l)
	})
	return l
}

func TestPhotoTimelineDateFiltersAndMonthSummary(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/jan-a.jpg", "Photos/jan-b.jpg", "Photos/feb.jpg", "Photos/unknown.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, date := range map[string]string{"Photos/jan-a.jpg": "2020-01-03T12:00:00Z", "Photos/jan-b.jpg": "2020-01-14T12:00:00Z", "Photos/feb.jpg": "2020-02-02T12:00:00Z"} {
		captured, err := time.Parse(time.RFC3339, date)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.SetPhotoCapture(ctx, name, catalog.CaptureMetadata{Time: &captured, Source: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := l.PhotoTimelinePage(ctx, 1, "", "", "all", "2020-01")
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("January page=%+v err=%v", first, err)
	}
	if _, err := l.PhotoTimelinePage(ctx, 10, first.NextCursor, "", "all", "2020-02"); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("cursor reused with different date filter: %v", err)
	}
	second, err := l.PhotoTimelinePage(ctx, 10, first.NextCursor, "", "all", "2020-01")
	if err != nil || len(second.Items) != 1 {
		t.Fatalf("January continuation=%+v err=%v", second, err)
	}
	summary, err := l.PhotoDateSummary(ctx)
	if err != nil || len(summary.Months) != 2 || summary.UnknownDates != 1 || summary.Months[0].Month != "2020-02" || summary.Months[0].Count != 1 {
		t.Fatalf("date summary=%+v err=%v", summary, err)
	}
	if !PhotoDateFilter("2020-02") || PhotoDateFilter("2020-13") || PhotoDateFilter("2020-02-31") {
		t.Fatal("date filter validation is incorrect")
	}
	if !PhotoDateFilter("2020") || PhotoDateFilter("20x0") {
		t.Fatal("year date filter validation is incorrect")
	}
	year, err := l.PhotoTimelinePage(ctx, 10, "", "", "all", "2020")
	if err != nil || len(year.Items) != 3 {
		t.Fatalf("year page=%+v err=%v", year, err)
	}
}

func TestPhotoDetailIsOwnerIndexScoped(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	_, err := l.Put(ctx, "Photos/Trip/a.jpg", []byte("photo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Documents/a.jpg", []byte("other")); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("photo listing=%+v err=%v", page, err)
	}
	photoID := page.Items[0].ID
	item, err := l.PhotoDetail(ctx, photoID)
	if err != nil || item.Path != "Photos/Trip/a.jpg" || item.ID != photoID {
		t.Fatalf("photo detail=%+v err=%v", item, err)
	}
	favorite, err := l.SetPhotoFavorite(ctx, photoID, true)
	if err != nil || !favorite.Favorite {
		t.Fatalf("set favorite=%+v err=%v", favorite, err)
	}
	page, err = l.PhotoTimelinePage(ctx, 10, "", "", "favorites", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != photoID {
		t.Fatalf("favorites page=%+v err=%v", page, err)
	}
	if _, err := l.PhotoDetail(ctx, "missing"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown photo detail error=%v", err)
	}
}

func TestPhotoHiddenFoldersFilterEveryNormalIndexSurface(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	file, err := l.Put(ctx, "Photos/Private/secret.jpg", []byte("image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.catalog.Get(file.Path)
	if _, err = l.Put(ctx, "Photos/Public.jpg", []byte("public image")); err != nil {
		t.Fatal(err)
	}
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	folder, ok := l.catalog.Get("Photos/Private")
	if !ok || !folder.Hidden {
		t.Fatalf("hidden flag not persisted in catalog: %+v", folder)
	}
	normal, err := l.PhotoTimelinePage(ctx, 20, "", "", "all", "")
	if err != nil || len(normal.Items) != 1 || normal.Items[0].Path != "Photos/Public.jpg" {
		t.Fatalf("normal timeline=%+v err=%v", normal, err)
	}
	hidden, err := l.PhotoTimelinePage(ctx, 20, "", "", "hidden", "")
	if err != nil || len(hidden.Items) != 1 || hidden.Items[0].ID != file.EntryID {
		t.Fatalf("hidden timeline=%+v err=%v", hidden, err)
	}
	if _, err = l.PhotoDetail(ctx, file.EntryID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("normal detail exposed hidden photo: %v", err)
	}
	if detail, err := l.PhotoDetail(ctx, file.EntryID, true); err != nil || detail.Path != file.Path {
		t.Fatalf("hidden detail=%+v err=%v", detail, err)
	}
	normalDates, err := l.PhotoDateSummary(ctx)
	if err != nil || normalDates.UnknownDates != 1 {
		t.Fatalf("normal date summary=%+v err=%v", normalDates, err)
	}
	hiddenDates, err := l.PhotoDateSummary(ctx, true)
	if err != nil || hiddenDates.UnknownDates != 1 {
		t.Fatalf("hidden date summary=%+v err=%v", hiddenDates, err)
	}
	albums, err := l.PhotoAlbums(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, album := range albums {
		if album.Path == "Photos/Private" {
			t.Fatalf("hidden folder leaked as album: %+v", album)
		}
	}
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/Private", false); err != nil {
		t.Fatal(err)
	}
	visible, err := l.PhotoTimelinePage(ctx, 20, "", "", "all", "")
	if err != nil || len(visible.Items) != 2 {
		t.Fatalf("unhidden timeline=%+v err=%v", visible, err)
	}
}
