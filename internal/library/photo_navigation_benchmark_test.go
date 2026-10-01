package library

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func BenchmarkPhotoNavigation(b *testing.B) {
	for _, size := range []int{37082, 100000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			l := newPhotoIndexTestLibrary(b)
			rows := make([]catalog.File, size)
			for i := range rows {
				captured := time.Date(2026-i%25, time.Month(1+i%12), 1+i%27, 12, 0, 0, 0, time.UTC)
				rows[i] = catalog.File{EntryID: fmt.Sprintf("entry-%06d", i), Revision: 1, Path: fmt.Sprintf("Photos/%06d.jpg", i), CaptureTime: &captured, Present: true}
			}
			sortPhotoRows(rows)
			l.photoMu.Lock()
			l.photoRows = rows
			l.photoReady = true
			l.photoEpoch = 1
			l.photoSortedEpoch = 1
			l.rebuildPhotoLookupsLocked()
			l.photoMu.Unlock()
			scope := PhotoScope{}
			if _, err := l.PhotoNavigationSummary(context.Background(), scope, ""); err != nil {
				b.Fatal(err)
			}
			times := make([]time.Duration, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				begin := time.Now()
				_, err := l.PhotoNavigation(context.Background(), PhotoNavigationRequest{At: "2018-06-15", Scope: scope})
				if err != nil {
					b.Fatal(err)
				}
				_, err = l.PhotoNavigationSummary(context.Background(), scope, "")
				if err != nil {
					b.Fatal(err)
				}
				times[i] = time.Since(begin)
			}
			b.StopTimer()
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			if len(times) > 0 {
				b.ReportMetric(float64(times[min(len(times)-1, len(times)*95/100)].Microseconds())/1000, "metadata-p95-ms")
			}
		})
	}
}
