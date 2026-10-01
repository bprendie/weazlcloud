package library

import (
	"context"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoDaySelectionPagesBeyondViewportAndUnknownDates(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	day, _ := time.Parse(time.RFC3339, "2024-05-12T10:00:00-04:00")
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg", "Photos/c.jpg", "Photos/unknown.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
		if name != "Photos/unknown.jpg" {
			if _, err := l.SetPhotoCapture(ctx, name, catalog.CaptureMetadata{Time: &day, Source: "test"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := make(map[string]bool)
	cursor := ""
	for {
		page, err := l.PhotoTimelinePage(ctx, 1, cursor, "", "all", "2024-05-12")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("repeated selection asset")
			}
			seen[item.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatalf("full day selected %d assets", len(seen))
	}
	unknown, err := l.PhotoTimelinePage(ctx, 1, "", "", "all", "unknown")
	if err != nil || len(unknown.Items) != 1 || unknown.Items[0].Path != "Photos/unknown.jpg" {
		t.Fatalf("unknown=%+v error=%v", unknown, err)
	}
	search, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "*.jpg", UnknownDates: true, Limit: 1})
	if err != nil || len(search.Items) != 1 || search.Items[0].Path != "Photos/unknown.jpg" {
		t.Fatalf("unknown search=%+v error=%v", search, err)
	}
}

func TestPhotoSearchContinuesAfterDeletedAnchor(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/a.jpg", "Photos/b.jpg", "Photos/c.jpg"} {
		if _, err := l.Put(ctx, name, []byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "*.jpg", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if err := l.Delete(first.Items[0].Path); err != nil {
		t.Fatal(err)
	}
	next, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "*.jpg", Limit: 10, Cursor: first.NextCursor})
	if err != nil || len(next.Items) != 2 {
		t.Fatalf("continued search=%+v error=%v", next, err)
	}
}

func TestPhotoCalendarUsesCaptureOffsetAcrossMidnight(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	file, err := l.Put(ctx, "Photos/late.jpg", []byte("photo"))
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.catalog.Get(file.Path)
	captured, _ := time.Parse(time.RFC3339, "2024-05-31T23:40:00-04:00")
	offset := -240
	_, err = l.UpdatePhoto(ctx, file.EntryID, PhotoUpdate{Capture: &catalog.CaptureMetadata{Time: &captured, OffsetMinutes: &offset, Source: "user", UserCorrected: true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoTimelinePage(ctx, 20, "", "", "all", "2024-05-31")
	if err != nil || len(page.Items) != 1 || page.Items[0].CapturedAt.Format(time.RFC3339) != "2024-05-31T23:40:00-04:00" {
		t.Fatalf("capture day=%+v error=%v", page, err)
	}
	summary, err := l.PhotoDateSummary(ctx)
	if err != nil || len(summary.Months) != 1 || summary.Months[0].Month != "2024-05" {
		t.Fatalf("capture month=%+v error=%v", summary, err)
	}
	search, err := l.PhotoSearchPage(ctx, PhotoSearchOptions{DateFrom: "2024-05-31", DateTo: "2024-05-31"})
	if err != nil || len(search.Items) != 1 {
		t.Fatalf("capture search=%+v error=%v", search, err)
	}
}
