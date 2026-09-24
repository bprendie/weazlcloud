package library

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const albumCacheLimit = 16 << 20

func IsPhotoAlbumMetadata(name string) bool {
	if !strings.HasPrefix(name, PhotosRoot) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(name, PhotosRoot), "/")
	return len(parts) == 2 && albumMetadataName(parts[1])
}

func (l *Library) albumCachePath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-albums.enc")
}

// Cache failures only cause a rebuild from the original library files.
// Album titles and descriptions never get a plaintext on-disk index.
func (l *Library) loadAlbumCache() {
	if l.albumMetadata != nil {
		return
	}
	l.albumMetadata = make(map[string]albumMetadata)
	f, err := os.Open(l.albumCachePath())
	if err != nil {
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, albumCacheLimit+1))
	if err != nil || len(raw) > albumCacheLimit {
		return
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return
	}
	defer cryptox.Zero(plain)
	var cache map[string]albumMetadata
	if json.Unmarshal(plain, &cache) == nil && len(cache) <= 2048 && cache != nil {
		l.albumMetadata = cache
	}
}

func (l *Library) saveAlbumCache() {
	plain, err := json.Marshal(l.albumMetadata)
	if err != nil {
		return
	}
	defer cryptox.Zero(plain)
	if len(plain) > albumCacheLimit-1024 {
		return
	}
	raw, err := l.vault.Wrap(plain)
	if err == nil {
		_ = cryptox.AtomicWrite(l.albumCachePath(), raw, 0o600)
	}
}

// RememberPhotoAlbumMetadata avoids restoring metadata via Restic when it has
// just passed through the importer. The live catalog must agree with its hash.
func (l *Library) RememberPhotoAlbumMetadata(name, hash string, raw []byte) {
	if !IsPhotoAlbumMetadata(name) || len(raw) > albumMetadataLimit {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return
	}
	f, ok := l.catalog.Get(name)
	if !ok || f.Hash != hash {
		return
	}
	l.loadAlbumCache()
	key := name + ":" + hash
	if _, ok := l.albumMetadata[key]; ok {
		return
	}
	if len(l.albumMetadata) >= 2048 {
		l.albumMetadata = make(map[string]albumMetadata)
	}
	l.albumMetadata[key] = parseAlbumMetadata(raw)
	l.saveAlbumCache()
}
