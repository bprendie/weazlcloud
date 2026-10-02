package library

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestThumbnailKeyUsesContentInsteadOfPath(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("thumb-test"), []byte("thumb-test")); err != nil {
		t.Fatal(err)
	}
	first, err := thumbnailKey(v, catalog.File{Hash: "same"}, 320)
	if err != nil {
		t.Fatal(err)
	}
	rename, err := thumbnailKey(v, catalog.File{Hash: "same"}, 320)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := thumbnailKey(v, catalog.File{Hash: "different"}, 320)
	if err != nil {
		t.Fatal(err)
	}
	if first != rename || first == replacement || strings.Contains(first, "same") {
		t.Fatal("thumbnail cache key did not follow content identity")
	}
}

func TestThumbnailCacheManagerEnforcesOwnerAndNodeCaps(t *testing.T) {
	oldOwner, oldFiles, oldNode := thumbnailMaxBytes, thumbnailMaxFiles, thumbnailNodeMaxBytes
	thumbnailMaxBytes, thumbnailMaxFiles, thumbnailNodeMaxBytes = 5, 10, 100
	defer func() { thumbnailMaxBytes, thumbnailMaxFiles, thumbnailNodeMaxBytes = oldOwner, oldFiles, oldNode }()
	cache := &thumbnailNodeCache{
		files: map[string]trackedThumbnail{}, ownerBytes: map[string]int64{}, ownerFiles: map[string]int{},
	}
	dir := t.TempDir()
	for i, n := range []int64{4, 4} {
		path := filepath.Join(dir, string(rune('a'+i)))
		if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
			t.Fatal(err)
		}
		cache.files[path] = trackedThumbnail{path: path, owner: "alice", size: n, when: time.Unix(int64(i), 0)}
		cache.ownerBytes["alice"] += n
		cache.ownerFiles["alice"]++
		cache.total += n
	}
	cache.evict("alice")
	if cache.ownerBytes["alice"] > thumbnailMaxBytes || cache.total > thumbnailNodeMaxBytes {
		t.Fatalf("caps not enforced: %+v", cache)
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

func TestMakeThumbnailJPEGAndRejectsHugeDecodedRaster(t *testing.T) {
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1600, 800)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	body, contentType, err := makeThumbnail(source.Bytes(), 320)
	if err != nil || contentType != "image/jpeg" {
		t.Fatalf("JPEG thumbnail type %q, error %v", contentType, err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || config.Width != 320 || config.Height != 160 {
		t.Fatalf("JPEG thumbnail config %+v, error %v", config, err)
	}
	header := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0x80, 0, 0, 0x80, 0, 8, 2, 0, 0, 0}
	if _, _, err := makeThumbnail(header, 320); err == nil {
		t.Fatal("large decoded image accepted")
	}
}

type countingThumbnailBackend struct {
	*isolatedLegacy
	reads int
}

func (b *countingThumbnailBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	b.reads++
	return b.isolatedLegacy.Read(ctx, ref, w)
}

func TestPhotoThumbnailReusesEncryptedPreviewAfterRename(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	backend := &countingThumbnailBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "repo")}}
	l.backend = backend
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 640, 480)), &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(context.Background(), "Photos/Trip/original.jpg", source.Bytes()); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoPage(context.Background(), 10, "", "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("photo index page %+v, %v", page, err)
	}
	first, kind, err := l.PhotoThumbnail(context.Background(), page.Items[0].ID, 320)
	if err != nil || kind != "image/jpeg" || len(first) == 0 {
		t.Fatalf("first preview %q, %d bytes, %v", kind, len(first), err)
	}
	reads := backend.reads
	if err := l.Rename(context.Background(), "Photos/Trip/original.jpg", "Photos/Trip/renamed.jpg"); err != nil {
		t.Fatal(err)
	}
	second, _, err := l.PhotoThumbnail(context.Background(), page.Items[0].ID, 320)
	if err != nil || !bytes.Equal(first, second) || backend.reads != reads {
		t.Fatalf("rename missed encrypted preview cache: reads %d -> %d, err %v", reads, backend.reads, err)
	}
	entries, err := os.ReadDir(l.thumbnailDir())
	if err != nil || len(entries) != 2 || strings.Contains(entries[0].Name(), page.Items[0].Path) {
		t.Fatalf("preview cache path leaked source path: entries=%v err=%v", entries, err)
	}
}

func TestKnownRasterDimensionsUseOneSourceRestore(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	backend := &countingThumbnailBackend{isolatedLegacy: &isolatedLegacy{root: filepath.Join(filepath.Dir(l.repo), "repo")}}
	l.backend = backend
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 640, 480)), &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(context.Background(), "Photos/one-read.jpg", source.Bytes()); err != nil {
		t.Fatal(err)
	}
	file, ok := l.catalog.Get("Photos/one-read.jpg")
	if !ok {
		t.Fatal("uploaded file missing from catalog")
	}
	if _, err := l.catalog.UpdateMedia(file.EntryID, file.Revision, catalog.MediaMetadata{Width: 640, Height: 480}); err != nil {
		t.Fatal(err)
	}
	before := backend.reads
	if _, _, err := l.Thumbnail(context.Background(), "Photos/one-read.jpg", 320); err != nil {
		t.Fatal(err)
	}
	if reads := backend.reads - before; reads != 1 {
		t.Fatalf("preview restored source %d times; want one", reads)
	}
}
