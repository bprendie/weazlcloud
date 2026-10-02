package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type measuredPreviewBackend struct {
	*isolatedLegacy
	reads atomic.Int64
	nanos atomic.Int64
}

func (b *measuredPreviewBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	start := time.Now()
	b.reads.Add(1)
	err := b.isolatedLegacy.Read(ctx, ref, w)
	b.nanos.Add(time.Since(start).Nanoseconds())
	return err
}

// This small reproducible local sample measures service/cache overhead. It does
// not claim to reproduce production pack/index costs or cold OS page-cache I/O.
func TestThumbnailPerformanceRecord(t *testing.T) {
	if os.Getenv("WEAZLCLOUD_MEASURE_PREVIEWS") != "1" {
		t.Skip("opt-in performance sample")
	}
	l := newPhotoIndexTestLibrary(t)
	backend := &measuredPreviewBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "repo")}}
	l.backend = backend
	ctx := context.Background()
	var paths []string
	for i := 0; i < 4; i++ {
		img := image.NewNRGBA(image.Rect(0, 0, 1200, 800))
		for y := 0; y < 800; y++ {
			for x := 0; x < 1200; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x + i*17), G: uint8(y + i*23), B: uint8(x*y + i), A: uint8(127 + (x+y)%129)})
			}
		}
		var data bytes.Buffer
		name := "Photos/measure-" + string(rune('a'+i))
		if i%2 == 0 {
			name += ".jpg"
			if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 85}); err != nil {
				t.Fatal(err)
			}
		} else {
			name += ".png"
			if err := png.Encode(&data, img); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := l.Put(ctx, name, data.Bytes()); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	start, before := time.Now(), backend.reads.Load()
	for _, name := range paths {
		for _, size := range []int{320, 1280} {
			if _, _, err := l.Thumbnail(ctx, name, size); err != nil {
				t.Fatal(err)
			}
		}
	}
	elapsed := time.Since(start)
	t.Logf("cold_derivatives assets=4 variants=8 wall_ms=%.3f assets_per_sec=%.3f source_reads=%d source_ms=%.3f", float64(elapsed.Microseconds())/1000, 4/elapsed.Seconds(), backend.reads.Load()-before, float64(backend.nanos.Load())/1e6)
	before = backend.reads.Load()
	samples := make([]time.Duration, 40)
	for i := range samples {
		start := time.Now()
		if _, _, err := l.Thumbnail(ctx, paths[i%len(paths)], 320); err != nil {
			t.Fatal(err)
		}
		samples[i] = time.Since(start)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("warm_derivatives samples=40 p50_ms=%.3f p95_ms=%.3f source_reads=%d", float64(samples[20].Microseconds())/1000, float64(samples[37].Microseconds())/1000, backend.reads.Load()-before)
	if backend.reads.Load() != before {
		t.Fatal("warm preview restored an original")
	}
	// All removals below apply only to this test's disposable derived cache.
	for _, workers := range []int{1, 2, 4} {
		if workers > previewPolicy.BackgroundWorkers {
			t.Logf("bundle_workers=%d not_measured resource ceiling", workers)
			continue
		}
		previewRAM.clearOwnerSession(l, nil)
		if err := os.RemoveAll(l.thumbnailDir()); err != nil {
			t.Fatal(err)
		}
		forgetThumbnailNode(l.thumbnailDir())
		thumbnailCacheEpoch.Add(1)
		start, before := time.Now(), backend.reads.Load()
		slots := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, name := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				f, err := l.Metadata(ctx, name)
				if err == nil {
					_, _, err = l.thumbnailFor(ctx, f, 320, true)
				}
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		t.Logf("retained_bundles workers=%d assets=4 variants=8 wall_ms=%.3f assets_per_sec=%.3f source_reads=%d", workers, float64(elapsed.Microseconds())/1000, 4/elapsed.Seconds(), backend.reads.Load()-before)
		if backend.reads.Load()-before != 4 {
			t.Fatal("bundle restored source more than once")
		}
	}
	// Repeat the complete preparation against persisted variants, after clearing RAM.
	previewRAM.clearOwnerSession(l, nil)
	before = backend.reads.Load()
	start = time.Now()
	for _, name := range paths {
		f, err := l.Metadata(ctx, name)
		if err == nil {
			_, _, err = l.thumbnailFor(ctx, f, 320, true)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("warm_bundle_disk assets=4 wall_ms=%.3f source_reads=%d", float64(time.Since(start).Microseconds())/1000, backend.reads.Load()-before)
	if backend.reads.Load() != before {
		t.Fatal("warm bundle used an original")
	}

}
