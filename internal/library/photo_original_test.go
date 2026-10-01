package library

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoOriginalPinsRevisionWithoutHoldingMetadataLock(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	original := []byte("original photo bytes")
	file, err := l.Put(ctx, "Photos/a.jpg", original)
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.catalog.Get(file.Path)
	err = l.ReadPhotoOriginal(ctx, file.EntryID, false, func(item PhotoItem, source PhotoRangeSource) error {
		if item.ID != file.EntryID {
			t.Fatal("wrong original identity")
		}
		// A metadata mutation completes while the immutable read is admitted.
		if _, err := l.Put(ctx, file.Path, []byte("replacement")); err != nil {
			return err
		}
		var out bytes.Buffer
		if err := source(1, 7, &out); err != nil {
			return err
		}
		if !bytes.Equal(out.Bytes(), original[1:8]) {
			t.Fatalf("read followed a replacement: %q", out.Bytes())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPhotoOriginalRejectsNormalHiddenReadAndInvalidRange(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	file, err := l.Put(ctx, "Photos/Private/a.jpg", []byte("photo"))
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.catalog.Get(file.Path)
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	if err := l.ReadPhotoOriginal(ctx, file.EntryID, false, func(PhotoItem, PhotoRangeSource) error { t.Fatal("hidden read admitted"); return nil }); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("normal hidden read=%v", err)
	}
	if err := l.ReadPhotoOriginal(ctx, file.EntryID, true, func(_ PhotoItem, source PhotoRangeSource) error {
		var out bytes.Buffer
		if err := source(0, 100, &out); !errors.Is(err, ErrPhotoCursor) {
			t.Fatalf("invalid range=%v", err)
		}
		return source(0, 5, &out)
	}); err != nil {
		t.Fatal(err)
	}
}
