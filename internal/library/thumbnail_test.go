package library

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestThumbnailKeyChangesWithCatalogVersion(t *testing.T) {
	first := thumbnailKey("photos/a.png", catalog.File{Hash: "one", Snap: "snap", Object: "a"}, 320)
	second := thumbnailKey("photos/a.png", catalog.File{Hash: "two", Snap: "snap", Object: "a"}, 320)
	if first == second {
		t.Fatal("thumbnail cache key did not change with the file version")
	}
}

func TestCleanupThumbnailCacheRemovesTempsAndPreservesCache(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, ".thumbnail-abandoned")
	cache := filepath.Join(dir, "preview.enc")
	if err := os.WriteFile(temp, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := CleanupThumbnailCache(dir)
	if err != nil || reclaimed != int64(len("temporary")) {
		t.Fatalf("reclaimed=%d err=%v", reclaimed, err)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("temporary preview remains: %v", err)
	}
	if b, err := os.ReadFile(cache); err != nil || string(b) != "cache" {
		t.Fatalf("cache changed: %q, %v", b, err)
	}
}

func TestMakeThumbnailBoundsRasterWithoutSourceGrowth(t *testing.T) {
	data := make([]byte, 0)
	var source bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	data = source.Bytes()
	body, contentType, err := makeThumbnail(data, 320)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q", contentType)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 160 {
		t.Fatalf("thumbnail bounds = %v", decoded.Bounds())
	}
}
