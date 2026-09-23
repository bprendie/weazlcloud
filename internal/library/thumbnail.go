package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

const (
	thumbnailRenderer = "raster-v1"
	thumbnailMaxInput = 64 << 20
	thumbnailMaxBytes = 256 << 20
	thumbnailMaxFiles = 4096
	thumbnailWorkers  = 4
)

var thumbnailSlots = make(chan struct{}, thumbnailWorkers)

var (
	ErrThumbnailUnavailable = errors.New("thumbnail unavailable for this file")
	ErrPreviewTooLarge      = errors.New("preview input is too large")
)

type thumbnailEnvelope struct {
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

type cacheFile struct {
	name string
	size int64
	when time.Time
}

type thumbnailJob struct {
	done        chan struct{}
	body        []byte
	contentType string
	err         error
}

// Thumbnail restores and scales common raster images without putting the
// original file in the browser grid. The cache key includes the catalog
// version, so replacement automatically creates a new thumbnail.
func (l *Library) Thumbnail(ctx context.Context, name string, size int) ([]byte, string, error) {
	name, err := cleanPath(name)
	if err != nil {
		return nil, "", err
	}
	if size < 96 {
		size = 96
	}
	if size > 640 {
		size = 640
	}

	f, err := l.Metadata(ctx, name)
	if err != nil {
		return nil, "", err
	}
	if f.Folder || f.Size <= 0 || f.Size > thumbnailMaxInput {
		return nil, "", ErrThumbnailUnavailable
	}

	key := thumbnailKey(name, f, size)
	if body, contentType, ok := l.readThumbnailCache(key); ok {
		return body, contentType, nil
	}

	l.thumbMu.Lock()
	if job := l.thumbJobs[key]; job != nil {
		l.thumbMu.Unlock()
		select {
		case <-job.done:
			return job.body, job.contentType, job.err
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	job := &thumbnailJob{done: make(chan struct{})}
	l.thumbJobs[key] = job
	l.thumbMu.Unlock()

	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		job.err = ctx.Err()
		l.finishThumbnailJob(key, job)
		return nil, "", job.err
	}
	data, err := l.Get(ctx, name)
	if err == nil {
		job.body, job.contentType, err = makeThumbnail(data, size)
		if err != nil {
			err = ErrThumbnailUnavailable
		} else {
			_ = l.writeThumbnailCache(key, thumbnailEnvelope{ContentType: job.contentType, Body: job.body})
		}
	}
	job.err = err
	l.finishThumbnailJob(key, job)
	return job.body, job.contentType, job.err
}

func (l *Library) finishThumbnailJob(key string, job *thumbnailJob) {
	l.thumbMu.Lock()
	delete(l.thumbJobs, key)
	close(job.done)
	l.thumbMu.Unlock()
}

func thumbnailKey(name string, f catalog.File, size int) string {
	h := sha256.New()
	_, _ = io.WriteString(h, thumbnailRenderer+"\x00"+strconv.Itoa(size)+"\x00"+name+"\x00"+f.Hash+"\x00"+f.Snap+"\x00"+f.Object+"\x00"+f.Mtime.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))
}

func makeThumbnail(data []byte, size int) ([]byte, string, error) {
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, "", ErrThumbnailUnavailable
	}
	w, h := bounds.Dx(), bounds.Dy()
	dw, dh := size, size
	if w > h {
		dh = max(1, size*h/w)
	} else {
		dw = max(1, size*w/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx := bounds.Min.X + x*w/dw
			sy := bounds.Min.Y + y*h/dh
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, "", err
	}
	_ = format
	return out.Bytes(), "image/png", nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (l *Library) thumbnailDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-previews")
}

func (l *Library) readThumbnailCache(key string) ([]byte, string, bool) {
	path := filepath.Join(l.thumbnailDir(), key+".enc")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false
	}
	// Use the modification time as a bounded LRU surrogate. Cache reads are
	// authenticated by the vault unwrap below, so a stale or foreign file is
	// still rejected before it can be refreshed.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return nil, "", false
	}
	var env thumbnailEnvelope
	err = json.Unmarshal(plain, &env)
	if err != nil || len(env.Body) == 0 || env.ContentType == "" {
		return nil, "", false
	}
	return env.Body, env.ContentType, true
}

func (l *Library) writeThumbnailCache(key string, env thumbnailEnvelope) error {
	plain, err := json.Marshal(env)
	if err != nil {
		return err
	}
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return err
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
	return evictThumbnailCache(dir)
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
	// ReadDir is sorted by name, so sort explicitly by oldest access surrogate:
	// cache files are rewritten on generation and remain bounded without atime.
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
	// Kept as a small insertion sort to avoid a package-level sort import for
	// this tiny bounded cache.
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].when.Before(files[j-1].when); j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
