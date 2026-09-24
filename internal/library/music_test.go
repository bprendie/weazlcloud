package library

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type musicBackend struct {
	isolatedLegacy
	reads atomic.Int32
}

func (b *musicBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	b.reads.Add(1)
	return b.isolatedLegacy.Read(ctx, ref, w)
}

func TestMusicPrivateCacheReplacementAndFallback(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("music-test"), []byte("music-test")); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	b := &musicBackend{isolatedLegacy: isolatedLegacy{root: filepath.Join(root, "library")}}
	l.backend = b
	ctx := context.Background()
	body, err := os.ReadFile("../music/testdata/tagged.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Put(ctx, "track.mp3", body); err != nil {
		t.Fatal(err)
	}
	p, err := l.Music(ctx, "track.mp3")
	if err != nil || p.Title != "Midnight <Signal>" || !strings.HasPrefix(p.Artwork, "data:image/png;base64,") {
		t.Fatalf("preview %+v %v", p, err)
	}
	reads := b.reads.Load()
	if _, err = l.Music(ctx, "track.mp3"); err != nil || b.reads.Load() != reads {
		t.Fatal("cache reread audio", err)
	}
	entries, _ := os.ReadDir(l.thumbnailDir())
	if len(entries) == 0 {
		t.Fatal("no cache")
	}
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(l.thumbnailDir(), entry.Name()))
		if bytes.Contains(raw, []byte("Midnight")) || bytes.Contains(raw, []byte("data:image")) {
			t.Fatal("plaintext music cache")
		}
	}
	v.Lock()
	if _, err = l.Music(ctx, "track.mp3"); err == nil {
		t.Fatal("locked cache exposed tags")
	}
	if err = v.Unlock([]byte("music-test")); err != nil {
		t.Fatal(err)
	}
	// Same path, different hash must not reuse the first file's art or tags.
	if _, err = l.Put(ctx, "track.mp3", []byte("untagged audio content")); err != nil {
		t.Fatal(err)
	}
	p, err = l.Music(ctx, "track.mp3")
	if err != nil || p.Title != "" || p.Artwork != "" {
		t.Fatal("stale preview", err)
	}
	reads = b.reads.Load()
	if _, err = l.Music(ctx, "track.mp3"); err != nil || b.reads.Load() != reads {
		t.Fatal("negative result not cached")
	}
	if err = l.Delete("track.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Music(ctx, "track.mp3"); err == nil {
		t.Fatal("deleted file exposed cache")
	}
}
