package library

import (
	"context"
	"io"
	"testing"
	"time"
)

type archiveBrowseWriter struct {
	l       *Library
	checked bool
	t       *testing.T
}

func (w *archiveBrowseWriter) Write(body []byte) (int, error) {
	if !w.checked {
		w.checked = true
		done := make(chan error, 1)
		go func() { _, err := w.l.PhotoPage(context.Background(), 20, "", ""); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				return 0, err
			}
		case <-time.After(time.Second):
			w.t.Fatal("ZIP streaming blocked interactive photo metadata")
		}
	}
	return io.Discard.Write(body)
}

func TestArchiveStreamingLeavesPhotoMetadataAvailable(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.Put(ctx, "Photos/image.jpg", []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	manifest, err := l.PrepareArchive(ctx, []string{"Photos/image.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	writer := &archiveBrowseWriter{l: l, t: t}
	if _, _, err := l.WriteArchive(ctx, manifest, writer); err != nil {
		t.Fatal(err)
	}
	if !writer.checked {
		t.Fatal("archive did not stream")
	}
}

func TestPhotoAroundDeletedAnchorUsesVisibleNeighbor(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg", "Photos/c.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := page.Items[1].ID
	if err := l.Delete(page.Items[1].Path); err != nil {
		t.Fatal(err)
	}
	around, err := l.PhotoTimelinePageAround(ctx, 2, "", "all", "", id)
	if err != nil || len(around.Items) != 2 {
		t.Fatalf("deleted anchor recovery=%+v %v", around, err)
	}
	for _, item := range around.Items {
		if item.ID == id {
			t.Fatal("deleted item resurfaced")
		}
	}
}
