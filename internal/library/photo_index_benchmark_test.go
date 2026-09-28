package library

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func BenchmarkPhotoPage100K(b *testing.B) {
	l := newPhotoIndexTestLibrary(b)
	rows := make([]catalog.File, 100_000)
	for i := range rows {
		rows[i] = catalog.File{
			EntryID: fmt.Sprintf("entry-%06d", i),
			Path:    fmt.Sprintf("Photos/Year/album-%03d/photo-%06d.jpg", i%100, i),
			Mtime:   time.Unix(int64(len(rows)-i), 0),
			Size:    1024,
			Present: true,
		}
	}
	l.photoMu.Lock()
	l.photoRows = rows
	l.photoReady = true
	l.photoEpoch = 1
	l.photoSortedEpoch = 1
	l.rebuildPhotoLookupsLocked()
	l.photoMu.Unlock()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.PhotoPage(context.Background(), PhotoPageDefault, "", ""); err != nil {
			b.Fatal(err)
		}
	}
}
