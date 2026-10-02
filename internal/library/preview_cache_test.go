package library

import (
	"bytes"
	"context"
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
	write := func(key string) {
		finish, err := reserveThumbnail(root, key, 4, 320)
		if err != nil {
			t.Error(err)
			return
		}
		err = os.WriteFile(filepath.Join(root, key+".enc"), []byte("data"), 0600)
		finish(err == nil)
		if err != nil {
			t.Error(err)
		}
	}
	write("a")
	write("b")
	write("c")
	touchThumbnail(root, "a", 320)
	write("d")
	if _, err := os.Stat(filepath.Join(root, "b.enc")); !os.IsNotExist(err) {
		t.Fatal("cold entry survived", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.enc")); err != nil {
		t.Fatal("warm entry evicted", err)
	}
	var wg sync.WaitGroup
	for _, key := range []string{"e", "f", "g", "h"} {
		wg.Add(1)
		go func() { defer wg.Done(); write(key) }()
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
