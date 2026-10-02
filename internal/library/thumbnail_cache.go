package library

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"strings"

	"golang.org/x/sys/unix"
)

var thumbnailWriteMu sync.RWMutex
var thumbnailKeyLocks [64]sync.Mutex
var ErrPreviewCacheSkipped = errors.New("preview could not be retained in cache")

type cacheFile struct {
	name string
	size int64
	when time.Time
}

func (l *Library) thumbnailDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-previews")
}

func (l *Library) readThumbnailCache(key string) ([]byte, string, bool) {
	env, ok := l.readThumbnailEnvelope(key)
	return env.Body, env.ContentType, ok
}
func (l *Library) readThumbnailEnvelope(key string) (thumbnailEnvelope, bool) {
	if env, ok := previewRAM.get(l, key); ok {
		touchThumbnail(l.thumbnailDir(), key, env.Size)
		return env, true
	}
	path := filepath.Join(l.thumbnailDir(), key+".enc")
	file, err := os.Open(path)
	if err != nil {
		return thumbnailEnvelope{}, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, thumbnailMaxOutput+1))
	if err != nil || len(raw) > thumbnailMaxOutput {
		return thumbnailEnvelope{}, false
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return thumbnailEnvelope{}, false
	}
	defer clear(plain)
	var env thumbnailEnvelope
	err = json.Unmarshal(plain, &env)
	if err != nil || len(env.Body) == 0 || env.ContentType == "" {
		return thumbnailEnvelope{}, false
	}
	touchThumbnail(l.thumbnailDir(), key, env.Size)
	previewRAM.put(l, key, env)
	return env, true
}

func (l *Library) writeThumbnailCache(key string, env thumbnailEnvelope) error {
	thumbnailWriteMu.RLock()
	defer thumbnailWriteMu.RUnlock()
	var stripe uint64
	for _, b := range []byte(key) {
		stripe = stripe*31 + uint64(b)
	}
	keyLock := &thumbnailKeyLocks[stripe%64]
	keyLock.Lock()
	defer keyLock.Unlock()
	plain, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(plain) > thumbnailMaxOutput-1024 {
		clear(plain)
		return ErrPreviewCacheSkipped
	}
	defer clear(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	finish, err := reserveThumbnail(l.thumbnailDir(), key, int64(len(wrapped)), env.Size)
	if err != nil {
		return err
	}
	success := false
	defer func() { finish(success) }()
	dir := l.thumbnailDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".thumbnail-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(wrapped); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, key+".enc")); err != nil {
		return err
	}
	if err := syncPreviewDirectory(dir); err != nil {
		return err
	}
	success = true
	previewRAM.put(l, key, env)
	return nil
}

func thumbnailCacheHasHeadroom(dir string, requested int64) bool {
	var stat unix.Statfs_t
	if unix.Statfs(dir, &stat) != nil {
		return false
	}
	available := int64(stat.Bavail) * int64(stat.Bsize)
	total := int64(stat.Blocks) * int64(stat.Bsize)
	reserve := total * 3 / 100
	return available-requested >= reserve
}

func evictThumbnailCache(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]cacheFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			continue
		}
		files = append(files, cacheFile{name: entry.Name(), size: info.Size(), when: info.ModTime()})
		total += info.Size()
	}
	if (thumbnailMaxBytes == 0 || total <= thumbnailMaxBytes) && len(files) <= thumbnailMaxFiles {
		return nil
	}
	// Evict oldest generated previews first. Reads do not touch mtimes, avoiding
	// a metadata write on every grid tile cache hit.
	sortCacheFiles(files)
	for _, f := range files {
		if (thumbnailMaxBytes == 0 || total <= thumbnailMaxBytes) && len(files) <= thumbnailMaxFiles {
			break
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err == nil {
			total -= f.size
			files = files[1:]
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func sortCacheFiles(files []cacheFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
}

func syncPreviewDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
