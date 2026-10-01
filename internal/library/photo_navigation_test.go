package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoNavigationScopedCalendarSeekAndContinuation(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	ids := map[string]string{}
	for name, stamp := range map[string]string{"Photos/a.jpg": "2018-06-15T02:00:00Z", "Photos/b.jpg": "2018-06-14T23:00:00Z", "Photos/c.jpg": "2020-01-01T01:00:00Z", "Photos/Private/h.jpg": "1999-01-01T00:00:00Z", "Photos/u.jpg": ""} {
		file, err := l.Put(ctx, name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
		file, _ = l.Metadata(ctx, name)
		ids[name] = file.EntryID
		if stamp != "" {
			instant, _ := time.Parse(time.RFC3339, stamp)
			offset := 0
			if name == "Photos/a.jpg" {
				offset = -300
			}
			_, err = l.SetPhotoCapture(ctx, name, catalog.CaptureMetadata{Time: &instant, OffsetMinutes: &offset, Source: "test"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	_, _ = l.SetPhotoFolderHidden(ctx, "Photos/Private", true)
	summary, err := l.PhotoNavigationSummary(ctx, PhotoScope{}, "")
	if err != nil || summary.Total != 4 || summary.KnownDates != 3 || summary.UnknownDates != 1 || len(summary.Months) != 2 {
		t.Fatalf("summary %+v %v", summary, err)
	}
	raw, _ := json.Marshal(PhotoNavigationDates{Months: []PhotoNavigationBucket{}})
	if string(raw) == "null" {
		t.Fatal("null bucket collection")
	}
	page, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{At: "2018-06-14", Limit: 1})
	if err != nil || page.AnchorID != ids["Photos/a.jpg"] || page.Total != 4 || page.PreviousCursor == "" || page.NextCursor == "" {
		t.Fatalf("seek %+v %v", page, err)
	}
	next, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{Cursor: page.NextCursor, Limit: 2})
	if err != nil || len(next.Items) != 2 || next.Items[1].ID != ids["Photos/u.jpg"] {
		t.Fatalf("continuation %+v %v", next, err)
	}
	previous, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{Cursor: page.PreviousCursor, Limit: 2})
	if err != nil || len(previous.Items) != 1 || previous.Items[0].ID != ids["Photos/c.jpg"] {
		t.Fatalf("previous %+v %v", previous, err)
	}
	if _, err = l.PhotoNavigation(ctx, PhotoNavigationRequest{Scope: PhotoScope{Mode: "hidden"}, Cursor: page.NextCursor}); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatal("cross-scope cursor accepted", err)
	}
	if _, err = newPhotoIndexTestLibrary(t).PhotoNavigation(ctx, PhotoNavigationRequest{Cursor: page.NextCursor}); !errors.Is(err, ErrPhotoCursorStale) {
		t.Fatal("cross-owner cursor accepted", err)
	}
	unknown, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{At: "unknown"})
	if err != nil || unknown.AnchorID != ids["Photos/u.jpg"] {
		t.Fatal(unknown, err)
	}
	empty, err := l.PhotoNavigationSummary(ctx, PhotoScope{Search: true, Filter: PhotoSearchOptions{Query: "absent"}}, "")
	if err != nil || empty.Total != 0 || empty.Months == nil {
		t.Fatal(empty, err)
	}
}
func TestPhotoNavigationDateCorrectionRetainsCursorAndScope(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for i, stamp := range []string{"2018-01-01T00:00:00Z", "2020-01-01T00:00:00Z", "2022-01-01T00:00:00Z"} {
		name := []string{"Photos/first.jpg", "Photos/middle.jpg", "Photos/last.jpg"}[i]
		_, _ = l.Put(ctx, name, []byte(name))
		instant, _ := time.Parse(time.RFC3339, stamp)
		_, _ = l.SetPhotoCapture(ctx, name, catalog.CaptureMetadata{Time: &instant, Source: "test"})
	}
	first, _ := l.PhotoNavigation(ctx, PhotoNavigationRequest{Limit: 1})
	if err := l.Delete(first.Items[0].Path); err != nil {
		t.Fatal(err)
	}
	next, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{Cursor: first.NextCursor, Limit: 1})
	if err != nil || len(next.Items) != 1 || next.Items[0].Path != "Photos/middle.jpg" {
		t.Fatal(next, err)
	}
	scope := PhotoScope{Search: true, Filter: PhotoSearchOptions{Query: "middle*"}}
	result, err := l.PhotoNavigation(ctx, PhotoNavigationRequest{Scope: scope, At: "1900-01-01"})
	if err != nil || result.Total != 1 || result.Items[0].Path != "Photos/middle.jpg" {
		t.Fatal(result, err)
	}
}
