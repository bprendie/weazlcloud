package library

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoCursorFindsNeighborsAfterAnchorDeletion(t *testing.T) {
	for _, mode := range []string{"all", "recent", "favorites"} {
		t.Run(mode, func(t *testing.T) {
			l := newPhotoIndexTestLibrary(t)
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				name := fmt.Sprintf("Photos/Trip/%d.jpg", i)
				if _, err := l.Put(ctx, name, []byte(name)); err != nil {
					t.Fatal(err)
				}
				file, _ := l.catalog.Get(name)
				favorite := true
				captured := time.Date(2020, 1, i+1, 12, 0, 0, 0, time.UTC)
				if _, err := l.UpdatePhoto(ctx, file.EntryID, PhotoUpdate{Favorite: &favorite, Capture: &catalog.CaptureMetadata{Time: &captured, Source: "test"}}, false); err != nil {
					t.Fatal(err)
				}
			}
			first, err := l.PhotoTimelinePage(ctx, 2, "", "Photos/Trip", mode, "")
			if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
				t.Fatalf("first=%+v err=%v", first, err)
			}
			expected, err := l.PhotoTimelinePage(ctx, 2, first.NextCursor, "Photos/Trip", mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := l.Delete(first.Items[1].Path); err != nil {
				t.Fatal(err)
			}
			continued, err := l.PhotoTimelinePage(ctx, 2, first.NextCursor, "Photos/Trip", mode, "")
			if err != nil || len(continued.Items) != 2 || continued.Items[0].ID != expected.Items[0].ID || continued.Items[1].ID != expected.Items[1].ID {
				t.Fatalf("deleted anchor skipped neighbors: page=%+v expected=%+v err=%v", continued, expected, err)
			}
		})
	}
}
