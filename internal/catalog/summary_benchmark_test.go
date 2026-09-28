package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkSummary97kEntries(b *testing.B) {
	c := &Catalog{files: make([]File, 0, 97000)}
	for i := 0; i < 97000; i++ {
		c.files = append(c.files, File{
			Path: fmt.Sprintf("Photos/%03d/%06d.jpg", i%400, i),
			Size: int64(1 + i%5000000), Hash: fmt.Sprintf("%08x", i%80000),
			Mtime: time.Unix(int64(i), 0), Present: true,
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Summary()
	}
}

func BenchmarkWildcardMatching97kEntries(b *testing.B) {
	c := &Catalog{files: make([]File, 97000)}
	for i := range c.files {
		c.files[i] = File{Path: fmt.Sprintf("Photos/%03d/family-%06d.jpg", i%400, i), Present: true, Hash: "fixture"}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Matching(context.Background(), func(f File) bool { return strings.Contains(f.Path, "family-0001") }); err != nil {
			b.Fatal(err)
		}
	}
}
