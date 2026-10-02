package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"io"
	"testing"
)

type unsupportedReadCounter struct {
	Backend
	reads int
}

func (b *unsupportedReadCounter) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	b.reads++
	return errors.New("unexpected original read")
}
func (b *unsupportedReadCounter) ReadRange(ctx context.Context, ref catalog.Reference, off, n int64, w io.Writer) error {
	b.reads++
	return errors.New("unexpected original range read")
}
func TestUnsupportedOriginalProjectionAndAllThumbnailPaths(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.PhotoCollections(ctx, "", nil, 1); err != nil {
		t.Fatal(err)
	}
	counter := &unsupportedReadCounter{Backend: l.backend}
	l.backend = counter
	for _, tc := range []struct {
		name, mime string
		marked     bool
	}{{"Photos/a.dng", "image/dng", false}, {"Photos/b.mov.opaque", "application/octet-stream", false}, {"Photos/c.jpg", "application/octet-stream", true}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := l.catalog.Put(catalog.File{Path: tc.name, Size: 12, Hash: tc.name, Snap: "fixture", PhotoPreviewUnsupported: tc.marked}); err != nil {
				t.Fatal(err)
			}
			f, _ := l.catalog.Get(tc.name)
			l.buildPhotoIndex()
			key, err := thumbnailKey(l.vault, f, 320)
			if err != nil {
				t.Fatal(err)
			}
			// Even a previously cached derivative may not override unsupported state.
			if err = l.writeThumbnailCache(key, thumbnailEnvelope{ContentType: "image/png", Body: []byte("cached"), Size: 320}); err != nil {
				t.Fatal(err)
			}
			item, err := l.PhotoDetail(ctx, f.EntryID)
			if err != nil || !item.PreviewUnsupported || item.MediaType != tc.mime || item.PreviewIdentity != "" || len(item.ThumbHash) != 0 {
				t.Fatalf("projection: %+v %v", item, err)
			}
			calls := []func() error{
				func() error { _, _, e := l.Thumbnail(ctx, f.Path, 320); return e },
				func() error { _, _, e := l.PhotoThumbnail(ctx, f.EntryID, 320); return e },
				func() error { _, _, e := l.PhotoThumbnailVisible(ctx, f.EntryID, 320, false); return e },
				func() error { _, _, e := l.thumbnailFor(ctx, f, 320, true); return e },
				func() error { _, _, release, e := l.renderThumbnail(ctx, f, 320, false); release(); return e },
				func() error { _, release, e := l.bundleSource(ctx, f, true); release(); return e },
				func() error { return l.generateBundle(ctx, f, nil) },
			}
			for i, call := range calls {
				if e := call(); !errors.Is(e, ErrThumbnailUnavailable) {
					t.Fatalf("thumbnail path %d: %v", i, e)
				}
			}
			state, err := l.PhotoProcessingState(ctx, f.EntryID)
			if err != nil || state != "unsupported" {
				t.Fatalf("state: %s %v", state, err)
			}
		})
	}
	if counter.reads != 0 {
		t.Fatalf("unsupported decoder path restored originals %d times", counter.reads)
	}
	if item := photoItemFromFile(catalog.File{Path: "Photos/normal.jpg"}); item.PreviewUnsupported || item.MediaType != "image/jpeg" {
		t.Fatal("normal JPEG projection changed")
	}
}
