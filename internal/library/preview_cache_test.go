package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestThumbnailAdaptiveTarget(t *testing.T) {
	for _, test := range []struct{ cache, free, want int64 }{{0, 0, 0}, {10, 90, 10}, {0, 1000 << 30, 64 << 30}, {10, -100, 0}} {
		if got := automaticThumbnailTarget(test.cache, test.free); got != test.want {
			t.Fatal(got, test)
		}
	}
}
func TestCacheConcurrentCapacityAndRecency(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".weazl-previews")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	oldNode := thumbnailNodeMaxBytes
	thumbnailNodeMaxBytes = 12
	defer func() { thumbnailNodeMaxBytes = oldNode; forgetThumbnailNode(root) }()
	write := func(key string, allowPressure bool) {
		finish, err := reserveThumbnail(root, key, 4, 320)
		if err != nil {
			// In-flight writes are pinned. Four concurrent four-byte writes cannot
			// all reserve a twelve-byte cache; refusing excess admission is correct.
			if allowPressure && errors.Is(err, ErrPreviewCacheSkipped) {
				return
			}
			t.Error(err)
			return
		}
		err = os.WriteFile(filepath.Join(root, key+".enc"), []byte("data"), 0600)
		finish(err == nil)
		if err != nil {
			t.Error(err)
		}
	}
	write("a", false)
	write("b", false)
	write("c", false)
	touchThumbnail(root, "a", 320)
	write("d", false)
	if _, err := os.Stat(filepath.Join(root, "b.enc")); !os.IsNotExist(err) {
		t.Fatal("cold entry survived", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.enc")); err != nil {
		t.Fatal("warm entry evicted", err)
	}
	var wg sync.WaitGroup
	for _, key := range []string{"e", "f", "g", "h"} {
		wg.Add(1)
		go func() { defer wg.Done(); write(key, true) }()
	}
	wg.Wait()
	c := thumbnailNode(dir)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total > 12 || c.pending != 0 {
		t.Fatal(c.total, c.pending)
	}
}
func TestPreviewRAMReclaimsAndSessionLockClears(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	env := thumbnailEnvelope{ContentType: "image/png", Body: bytes.Repeat([]byte{7}, 2048)}
	previewRAM.put(l, "fixture", env)
	if _, ok := previewRAM.get(l, "fixture"); !ok {
		t.Skip("RAM discovery disabled cache")
	}
	previewRAM.reclaim(previewMemory, previewMemory.limit)
	if _, ok := previewRAM.get(l, "fixture"); ok {
		t.Fatal("retained RAM blocked admission")
	}
	previewRAM.put(l, "fixture", env)
	l.vault.Lock()
	if _, ok := previewRAM.get(l, "fixture"); ok {
		t.Fatal("locked session returned cached bytes")
	}
}

func TestUnrelatedIngestRetainsWarmPreviewRAM(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	previewRAM.put(l, "fixture", thumbnailEnvelope{ContentType: "image/png", Body: []byte("warm")})
	if _, ok := previewRAM.get(l, "fixture"); !ok {
		t.Skip("RAM discovery disabled cache")
	}
	if _, err := l.Put(context.Background(), "Photos/new.jpg", []byte("new upload")); err != nil {
		t.Fatal(err)
	}
	if _, ok := previewRAM.get(l, "fixture"); !ok {
		t.Fatal("unrelated upload evicted warm previews")
	}
	l.publishChange(Change{Kind: "photo-visibility"})
	if _, ok := previewRAM.get(l, "fixture"); ok {
		t.Fatal("visibility change retained session bytes")
	}
}
func TestEncryptedPreviewManifestAndCorruption(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	f := putPreviewFixture(t, l, "Photos/private.png")
	if _, _, err := l.thumbnailFor(context.Background(), f, 320, true); err != nil {
		t.Fatal(err)
	}
	key, hash := l.photoPreviewHint(f)
	if len(hash) < 5 {
		t.Fatal("no hash")
	}
	path := filepath.Join(l.thumbnailDir(), key+".meta.enc")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, hash) || bytes.Contains(raw, []byte(f.Path)) {
		t.Fatal("manifest not encrypted")
	}
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	previewRAM.clearOwnerSession(l, nil)
	if _, hash = l.photoPreviewHint(f); len(hash) != 0 {
		t.Fatal("corrupt manifest accepted")
	}
	grid, _ := thumbnailKey(l.vault, f, 320)
	if !l.validCachedPreview(grid) {
		t.Fatal("manifest damage discarded valid grid")
	}
}

func TestCachePinnedCapacityRejectsExcessWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".weazl-previews")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	oldNode := thumbnailNodeMaxBytes
	thumbnailNodeMaxBytes = 12
	defer func() { thumbnailNodeMaxBytes = oldNode; forgetThumbnailNode(root) }()
	releases := []func(bool){}
	defer func() {
		for _, finish := range releases {
			finish(false)
		}
	}()
	for _, key := range []string{"a", "b", "c"} {
		finish, err := reserveThumbnail(root, key, 4, 320)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, finish)
	}
	if finish, err := reserveThumbnail(root, "excess", 4, 320); !errors.Is(err, ErrPreviewCacheSkipped) {
		if finish != nil {
			finish(false)
		}
		t.Fatalf("pinned capacity admitted excess: %v", err)
	}
	c := thumbnailNode(filepath.Dir(root))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != 12 || c.total != 0 {
		t.Fatalf("in-flight capacity pending=%d retained=%d", c.pending, c.total)
	}
}
