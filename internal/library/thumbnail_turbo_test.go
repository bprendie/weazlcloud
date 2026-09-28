package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func jpegFixture(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x/13 + y/17) % 256), 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func requireTurbo(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath(turboHelper)
	if err != nil {
		t.Skip("native preview helper not installed")
	}
	return path
}

func TestTurboJPEGDimensionsAndContent(t *testing.T) {
	requireTurbo(t)
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "turbo")
	for _, shape := range [][2]int{{1600, 800}, {700, 1500}, {64, 32}, {1, 100}, {4000, 3000}} {
		data := jpegFixture(t, shape[0], shape[1])
		for _, size := range []int{96, 320, 1280} {
			out, mime, err := renderThumbnailBytes(context.Background(), data, size)
			if err != nil || mime != "image/jpeg" {
				t.Fatalf("%v size%d: %s %v", shape, size, mime, err)
			}
			img, err := jpeg.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			w, h := size, size
			if shape[0] > shape[1] {
				h = max(1, size*shape[1]/shape[0])
			} else {
				w = max(1, size*shape[0]/shape[1])
			}
			if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
				t.Fatal(img.Bounds())
			}
			if shape[0] > 100 && shape[1] > 100 {
				r, g, _, _ := img.At(w/2, h/2).RGBA()
				if r < 100*257 || r > 155*257 || g < 100*257 || g > 155*257 {
					t.Fatalf("unexpected center color: %d %d", r, g)
				}
			}
		}
	}
}

func TestTurboRejectsTruncationAndCancellation(t *testing.T) {
	requireTurbo(t)
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "turbo")
	data := jpegFixture(t, 1600, 800)
	if _, _, err := renderThumbnailBytes(context.Background(), data[:len(data)/2], 320); err == nil {
		t.Fatal("truncated source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := renderThumbnailBytes(ctx, data, 320); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := renderThumbnailBytes(context.Background(), data, 1281); err == nil {
		t.Fatal("oversized output accepted")
	}
}

func TestPreviewRendererSelectionAndBoundedOutput(t *testing.T) {
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "invalid")
	if validatePreviewRenderer() == nil {
		t.Fatal("bad setting accepted")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "turbo")
	if validatePreviewRenderer() == nil {
		t.Fatal("missing required helper accepted")
	}
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "auto")
	if err := validatePreviewRenderer(); err != nil {
		t.Fatal(err)
	}
	data := jpegFixture(t, 200, 100)
	if _, _, err := renderThumbnailBytes(context.Background(), data, 96); err != nil {
		t.Fatal(err)
	}
	w := &previewOutput{limit: 10}
	if _, err := io.Copy(w, bytes.NewReader(make([]byte, 20))); err == nil || w.Len() > 10 {
		t.Fatal("output cap bypassed")
	}
}

func BenchmarkJPEGPreview(b *testing.B) {
	data := jpegFixture(b, 6000, 4000)
	for _, mode := range []string{"go", "turbo", "turbo-scalar"} {
		b.Run(mode, func(b *testing.B) {
			if mode != "go" {
				requireTurbo(b)
				b.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "turbo")
			} else {
				b.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
			}
			if mode == "turbo-scalar" {
				b.Setenv("JSIMD_FORCENONE", "1")
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := renderThumbnailBytes(context.Background(), data, 320); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestTurboWorkerCancelledWhileRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), turboHelper)
	// exec replaces the shell, matching the production helper's single-process contract.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := renderThumbnailTurbo(ctx, path, []byte("test"), 320, image.Config{Width: 320, Height: 320})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation: %v %v", err, time.Since(start))
	}
}
