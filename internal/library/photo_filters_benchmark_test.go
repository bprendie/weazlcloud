package library

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
)

// Measure warm owner metadata queries plus JSON encoding. This intentionally
// does not claim browser frame timing or HTTP/network latency.
func BenchmarkPhotoFilters32K(b *testing.B) {
	l := newPhotoIndexTestLibrary(b)
	ctx := context.Background()
	files := make([]catalog.File, 32_000)
	for i := range files {
		capture := time.Date(2010+i%16, time.Month(1+i%12), 1+i%27, 12, 0, 0, 0, time.UTC)
		files[i] = catalog.File{EntryID: fmt.Sprintf("entry-%06d", i), Revision: 1, Path: fmt.Sprintf("Photos/Trip%02d/photo-%06d.jpg", i%20, i), Size: 1024, Present: true, Favorite: i%10 == 0, Caption: "lake day", CaptureTime: &capture, Mtime: capture, ImportedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	}
	raw, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		b.Fatal(err)
	}
	wrapped, err := l.vault.Wrap(raw)
	clear(raw)
	if err != nil {
		b.Fatal(err)
	}
	if err := cryptox.AtomicWrite(filepath.Join(filepath.Dir(l.repo), "catalog.enc"), wrapped, 0600); err != nil {
		b.Fatal(err)
	}
	if err := l.Ensure(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := l.PhotoPage(ctx, 100, "", ""); err != nil {
		b.Fatal(err)
	}
	stopPhotoIndexSaveForTest(l)
	cases := []struct {
		name  string
		query func() (PhotoPage, error)
	}{
		{"timeline", func() (PhotoPage, error) { return l.PhotoPage(ctx, 100, "", "") }},
		{"favorites", func() (PhotoPage, error) { return l.PhotoTimelinePage(ctx, 100, "", "", "favorites", "") }},
		{"date", func() (PhotoPage, error) { return l.PhotoTimelinePage(ctx, 100, "", "", "all", "2024") }},
		{"album", func() (PhotoPage, error) { return l.PhotoPage(ctx, 100, "", "Photos/Trip00") }},
		{"search", func() (PhotoPage, error) {
			return l.PhotoSearchPage(ctx, PhotoSearchOptions{Query: "photo-*.jpg", Limit: 100})
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			if _, err := tc.query(); err != nil {
				b.Fatal(err)
			}
			times := make([]time.Duration, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				page, err := tc.query()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := json.Marshal(page); err != nil {
					b.Fatal(err)
				}
				times[i] = time.Since(start)
			}
			b.StopTimer()
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			b.ReportMetric(float64(times[min(len(times)-1, len(times)*95/100)].Nanoseconds())/1e6, "p95-ms")
		})
	}
}
