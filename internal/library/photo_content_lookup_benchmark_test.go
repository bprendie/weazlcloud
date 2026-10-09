package library

import (
	"context"
	"fmt"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// Metadata-only fixture: no generated originals or storage writes.
func BenchmarkPhotoContentLookup100K(b *testing.B) {
	l := newPhotoIndexTestLibrary(b)
	rows := make([]catalog.File, 100_000)
	keys := make([]PhotoContentKey, PhotoContentLookupLimit)
	for i := range rows {
		rows[i] = catalog.File{EntryID: fmt.Sprintf("entry-%06d", i),
			Path: fmt.Sprintf("Photos/%06d.jpg", i), Hash: fmt.Sprintf("%064x", i+1),
			Size: 1024, Present: true}
		if i < len(keys) {
			keys[i] = PhotoContentKey{rows[i].Hash, rows[i].Size}
		}
	}
	l.photoMu.Lock()
	l.photoRows, l.photoReady, l.photoEpoch = rows, true, 1
	l.rebuildPhotoLookupsLocked()
	l.photoMu.Unlock()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := l.PhotoContentLookup(context.Background(), keys, false)
		if err != nil || len(page.Results) != len(keys) || !page.Results[0].Exists {
			b.Fatalf("lookup failed: %v", err)
		}
	}
}
