package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func TestPreviewBundleRestoresOnceAndReusesAfterMetadataEdit(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	b := &measuredPreviewBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "repo")}}
	l.backend = b
	f := putPreviewFixture(t, l, "Photos/bundle.png")
	before := b.reads.Load()
	if _, _, err := l.thumbnailFor(context.Background(), f, 320, true); err != nil {
		t.Fatal(err)
	}
	if b.reads.Load()-before != 1 {
		t.Fatalf("source transfers=%d", b.reads.Load()-before)
	}
	key, hash := l.photoPreviewHint(f)
	if key == "" || len(hash) < 5 {
		t.Fatal("missing private placeholder manifest")
	}
	for _, size := range []int{320, 1280} {
		data, _, err := l.Thumbnail(context.Background(), f.Path, size)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || max(cfg.Width, cfg.Height) > size {
			t.Fatal(cfg, err)
		}
	}
	if _, err := l.SetPhotoFavorite(context.Background(), f.EntryID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.PhotoThumbnail(context.Background(), f.EntryID, 320); err != nil {
		t.Fatal(err)
	}
	if b.reads.Load()-before != 1 {
		t.Fatal("warm/metadata-only read fetched original")
	}
}
func TestPreviewBundleEmitsGridBeforeViewerAndKeepsAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 80})
	var source bytes.Buffer
	_ = png.Encode(&source, img)
	grid, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- RenderPhotoBundle(context.Background(), source.Bytes(), []int{320, 1280}, "raster", func(v previewrpc.Variant) error {
			if v.Size == 320 {
				decoded, _, err := image.Decode(bytes.NewReader(v.Body))
				if err != nil {
					return err
				}
				_, _, _, alpha := decoded.At(0, 0).RGBA()
				if alpha == 0 || alpha == 65535 {
					t.Error("transparency lost")
				}
				if len(v.ThumbHash) < 5 {
					t.Error("missing hash")
				}
				close(grid)
				<-release
			}
			return nil
		})
	}()
	select {
	case <-grid:
	case <-time.After(3 * time.Second):
		t.Fatal("grid blocked")
	}
	select {
	case <-done:
		t.Fatal("finished without viewer")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestBundleJoinCancellationDoesNotCancelSurvivor(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	b := &slowFirstPreviewBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "repo")}, started: make(chan struct{}), release: make(chan struct{})}
	l.backend = b
	f := putPreviewFixture(t, l, "Photos/coalesced.png")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, _, err := l.thumbnailFor(ctx, f, 320, false); first <- err }()
	<-b.started
	go func() { _, _, err := l.thumbnailFor(context.Background(), f, 1280, false); second <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		l.thumbMu.Lock()
		count := 0
		for _, job := range l.bundleJobs {
			count = job.waiters
		}
		l.thumbMu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if <-first == nil {
		t.Fatal("cancel ignored")
	}
	close(b.release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentOwnersReceivePreviewCapacity(t *testing.T) {
	owners := []*Library{newPhotoIndexTestLibrary(t), newPhotoIndexTestLibrary(t)}
	var wg sync.WaitGroup
	for _, l := range owners {
		f := putPreviewFixture(t, l, "Photos/fair.png")
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, _, err := l.thumbnailFor(ctx, f, 320, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
