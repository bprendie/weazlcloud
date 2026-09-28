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

var thumbnailWriteMu sync.Mutex
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
	path := filepath.Join(l.thumbnailDir(), key+".enc")
	file, err := os.Open(path)
	if err != nil {
		return nil, "", false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, thumbnailMaxOutput+1))
	if err != nil || len(raw) > thumbnailMaxOutput {
		return nil, "", false
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return nil, "", false
	}
	defer clear(plain)
	var env thumbnailEnvelope
	err = json.Unmarshal(plain, &env)
	if err != nil || len(env.Body) == 0 || env.ContentType == "" {
		return nil, "", false
	}
	return env.Body, env.ContentType, true
}

func (l *Library) writeThumbnailCache(key string, env thumbnailEnvelope) error {
	thumbnailWriteMu.Lock()
	defer thumbnailWriteMu.Unlock()
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
	if !thumbnailCacheHasHeadroom(filepath.Dir(l.repo), int64(len(wrapped))) {
		return ErrPreviewCacheSkipped
	}
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
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, key+".enc")); err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(dir, key+".enc"))
	if err == nil {
		l.accountThumbnail(dir, key+".enc", info.Size())
	}
	if _, err := os.Stat(filepath.Join(dir, key+".enc")); err != nil {
		return ErrPreviewCacheSkipped
	}
	return err
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
	if total <= thumbnailMaxBytes && len(files) <= thumbnailMaxFiles {
		return nil
	}
	// Evict oldest generated previews first. Reads do not touch mtimes, avoiding
	// a metadata write on every grid tile cache hit.
	sortCacheFiles(files)
	for _, f := range files {
		if total <= thumbnailMaxBytes && len(files) <= thumbnailMaxFiles {
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
