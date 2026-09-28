package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
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
	t.Cleanup(func() {
		l.photoMu.Lock()
		if l.photoSave != nil {
			l.photoSave.Stop()
		}
		l.photoMu.Unlock()
	})
	return l
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
	stale := first.NextCursor
	if _, err := l.Put(ctx, "Photos/Trip/new.webp", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PhotoPage(ctx, 1, stale, "Photos/Trip"); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatalf("mutation did not invalidate cursor: %v", err)
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
	cursor, err := first.encodePhotoCursor(photoCursor{Generation: 1, Offset: 1})
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
	t.Cleanup(func() {
		second.photoMu.Lock()
		if second.photoSave != nil {
			second.photoSave.Stop()
		}
		second.photoMu.Unlock()
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
	t.Cleanup(func() {
		third.photoMu.Lock()
		if third.photoSave != nil {
			third.photoSave.Stop()
		}
		third.photoMu.Unlock()
	})
	page, err = third.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("stale index cache was trusted: %+v, %v", page, err)
	}
}
