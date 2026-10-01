package library

import (
	"context"
	"errors"
	"fmt"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPhotoTimelineAroundAnchorHasBothDirections(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if _, err := l.Put(ctx, fmt.Sprintf("Photos/p-%d.jpg", i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	all, err := l.PhotoTimelinePage(ctx, 20, "", "", "all", "")
	if err != nil || len(all.Items) != 7 {
		t.Fatalf("all timeline=%+v err=%v", all, err)
	}
	anchor := all.Items[3].ID
	centered, err := l.PhotoTimelinePageAround(ctx, 3, "", "all", "", anchor)
	if err != nil || len(centered.Items) != 3 || centered.Items[1].ID != anchor || centered.PreviousCursor == "" || centered.NextCursor == "" {
		t.Fatalf("around page=%+v err=%v", centered, err)
	}
	earlier, err := l.PhotoTimelinePage(ctx, 3, centered.PreviousCursor, "", "all", "")
	if err != nil || len(earlier.Items) != 2 {
		t.Fatalf("earlier page=%+v err=%v", earlier, err)
	}
	later, err := l.PhotoTimelinePage(ctx, 3, centered.NextCursor, "", "all", "")
	if err != nil || len(later.Items) != 2 {
		t.Fatalf("later page=%+v err=%v", later, err)
	}
}

func stopPhotoIndexSaveForTest(l *Library) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = l.Drain(ctx)
	l.photoPrepMu.Lock()
	if l.photoPrepRunning {
		if l.photoPrepCancel != nil {
			l.photoPrepCancel()
		}
		l.photoPrep.Paused = true
		l.photoPrep.Status = "paused"
		l.savePhotoPreparationLocked()
	}
	l.photoPrepMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.photoPrepMu.Lock()
		running := l.photoPrepRunning
		l.photoPrepMu.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.photoMu.Lock()
	if l.photoSave != nil && l.photoSave.Stop() {
		l.photoSaveWG.Done()
	}
	l.photoMu.Unlock()
	l.photoSaveWG.Wait()
}

func TestPhotoPageBoundsFiltersAndInvalidatesCursors(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/Trip/a.jpg", "Photos/Trip/b.png", "Photos/Else/c.mov", "Drive/no.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := l.PhotoPage(ctx, 1, "", "Photos/Trip")
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page %+v, %v", first, err)
	}
	second, err := l.PhotoPage(ctx, 1000, first.NextCursor, "Photos/Trip")
	if err != nil || len(second.Items) != 1 || second.Items[0].Path == first.Items[0].Path {
		t.Fatalf("second page %+v, %v", second, err)
	}
	if _, err := l.PhotoPage(ctx, 1, first.NextCursor, "Photos/Else"); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("cursor filter mismatch: %v", err)
	}
	stable := first.NextCursor
	if _, err := l.Put(ctx, "Photos/Trip/new.webp", []byte("new")); err != nil {
		t.Fatal(err)
	}
	continued, err := l.PhotoPage(ctx, 1, stable, "Photos/Trip")
	if err != nil || len(continued.Items) != 1 {
		t.Fatalf("insert before anchor invalidated keyset cursor: page=%+v err=%v", continued, err)
	}
	page, err := l.PhotoPage(ctx, PhotoPageMaximum+10, "", "")
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("unfiltered page %+v, %v", page, err)
	}
}

func TestPhotoIndexTracksMoveAndDelete(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.Put(ctx, "Photos/Trip/a.jpg", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PhotoPage(ctx, 10, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := l.Rename(ctx, "Photos/Trip", "Photos/Travel"); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "Photos/Travel")
	if err != nil || len(page.Items) != 1 || page.Items[0].Path != "Photos/Travel/a.jpg" {
		t.Fatalf("renamed page %+v, %v", page, err)
	}
	if err := l.Delete("Photos/Travel"); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("deleted page %+v, %v", page, err)
	}
}

func TestPhotoCursorBelongsToVault(t *testing.T) {
	first := newPhotoIndexTestLibrary(t)
	first.catalog.Put(catalog.File{Path: "Photos/a.jpg", Size: 1, Present: true})
	first.buildPhotoIndex()
	cursor, err := first.encodePhotoCursor(photoCursor{Generation: 1, AfterID: "entry-id"})
	if err != nil {
		t.Fatal(err)
	}
	second := newPhotoIndexTestLibrary(t)
	if _, err := second.decodePhotoCursor(cursor); !errors.Is(err, ErrPhotoCursor) {
		t.Fatalf("other vault accepted cursor: %v", err)
	}
}

func TestPhotoIndexCacheIsEncryptedAndCheckedAgainstCatalog(t *testing.T) {
	first := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := first.Put(ctx, "Photos/Trip/a.jpg", []byte("photo")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PhotoPage(ctx, 10, "", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1600 * time.Millisecond)
	path := first.photoIndexCachePath()
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "Photos/Trip/a.jpg") {
		t.Fatalf("photo index cache not encrypted: %v", err)
	}
	second := New(first.repo, filepath.Join(filepath.Dir(first.repo), "catalog.enc"), first.vault)
	second.backend = &isolatedLegacy{root: filepath.Join(filepath.Dir(first.repo), "library")}
	second.photoAutoDisabled = true
	t.Cleanup(func() {
		stopPhotoIndexSaveForTest(second)
	})
	page, err := second.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Path != "Photos/Trip/a.jpg" {
		t.Fatalf("restored photo index %+v, %v", page, err)
	}
	if _, err := second.Put(ctx, "Photos/Trip/b.jpg", []byte("new")); err != nil {
		t.Fatal(err)
	}
	third := New(first.repo, filepath.Join(filepath.Dir(first.repo), "catalog.enc"), first.vault)
	third.backend = &isolatedLegacy{root: filepath.Join(filepath.Dir(first.repo), "library")}
	third.photoAutoDisabled = true
	t.Cleanup(func() {
		stopPhotoIndexSaveForTest(third)
	})
	page, err = third.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("stale index cache was trusted: %+v, %v", page, err)
	}
}
