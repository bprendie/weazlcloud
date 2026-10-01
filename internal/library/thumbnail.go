package library

import (
	"context"
	"encoding/hex"
	"errors"
	"log"
	"strconv"
	"sync"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const (
	thumbnailRenderer  = "media-v4"
	thumbnailMaxInput  = 64 << 20
	thumbnailMaxPixels = 32_000_000
	thumbnailMaxOutput = 8 << 20
)

var (
	thumbnailMaxBytes = previewLimit("WEAZLCLOUD_PREVIEW_OWNER_BYTES", 4<<30)
	thumbnailMaxFiles = int(previewLimit("WEAZLCLOUD_PREVIEW_OWNER_FILES", 100_000))
	previewPolicy     = defaultPreviewPolicy()
	thumbnailSlots    = make(chan struct{}, previewPolicy.RenderWorkers)
	thumbnailBackfill = make(chan struct{}, previewPolicy.BackgroundWorkers)
	thumbnailReaders  = make(chan struct{}, previewPolicy.SourceReaders)
	previewMemory     = newPreviewMemoryBudget(previewPolicy.MemoryBytes)
	previewLogOnce    sync.Once
)

var (
	ErrThumbnailUnavailable = errors.New("thumbnail unavailable for this file")
	ErrPreviewTooLarge      = errors.New("preview input is too large")
)

type thumbnailEnvelope struct {
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

type thumbnailJob struct {
	done        chan struct{}
	body        []byte
	contentType string
	err         error
	waiters     int
	cancel      context.CancelFunc
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
	if size > 1280 {
		size = 1280
	}

	f, err := l.Metadata(ctx, name)
	if err != nil {
		return nil, "", err
	}
	if f.Folder || f.Size <= 0 || f.Size > thumbnailMaxInput {
		return nil, "", ErrThumbnailUnavailable
	}

	return l.thumbnailFor(ctx, f, size, false)
}

func logPreviewPolicy() {
	previewLogOnce.Do(func() {
		mode, helper, _ := previewRenderer()
		log.Printf("photo preview renderer: mode=%s native_available=%t", mode, helper != "")
		log.Printf("photo preview resources: cpu ceiling=%d workers=%d background=%d readers=%d memory=%d reason=%s", previewPolicy.CPUBudget, previewPolicy.RenderWorkers, previewPolicy.BackgroundWorkers, previewPolicy.SourceReaders, previewPolicy.MemoryBytes, previewPolicy.Reason)
	})
}

// PhotoThumbnail resolves the current private index row before rendering. A
// stable entry ID avoids a full catalog reload for every visible photo tile.
func (l *Library) PhotoThumbnail(ctx context.Context, entryID string, size int) ([]byte, string, error) {
	return l.photoThumbnail(ctx, entryID, size, false)
}

// PhotoThumbnailVisible applies the normal-versus-Hidden Photos visibility
// boundary before resolving a derivative by asset ID.
func (l *Library) PhotoThumbnailVisible(ctx context.Context, entryID string, size int, hiddenView bool) ([]byte, string, error) {
	if entryID == "" || len(entryID) > 128 {
		return nil, "", ErrThumbnailUnavailable
	}
	l.mu.Lock()
	if !l.vault.Unlocked() {
		l.mu.Unlock()
		return nil, "", errors.New("vault is locked")
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		l.mu.Unlock()
		return nil, "", err
	}
	l.photoMu.Lock()
	indexed, found := l.photoByID[entryID]
	hidden := found && l.photoPathHiddenLocked(indexed.Path)
	l.photoMu.Unlock()
	current, exists := l.catalog.Get(indexed.Path)
	l.mu.Unlock()
	if !found || hidden != hiddenView || !exists || current.EntryID != indexed.EntryID || current.Revision != indexed.Revision || current.Hash != indexed.Hash || current.Folder || !photoMedia(current.Path) {
		return nil, "", ErrThumbnailUnavailable
	}
	return l.thumbnailFor(ctx, current, size, false)
}

func (l *Library) photoThumbnail(ctx context.Context, entryID string, size int, background bool) ([]byte, string, error) {
	if entryID == "" || len(entryID) > 128 {
		return nil, "", ErrThumbnailUnavailable
	}
	l.mu.Lock()
	if !l.vault.Unlocked() {
		l.mu.Unlock()
		return nil, "", errors.New("vault is locked")
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		l.mu.Unlock()
		return nil, "", err
	}
	l.photoMu.Lock()
	indexed, found := l.photoByID[entryID]
	l.photoMu.Unlock()
	current, exists := l.catalog.Get(indexed.Path)
	l.mu.Unlock()
	if !found || !exists || current.EntryID != indexed.EntryID || current.Revision != indexed.Revision || current.Hash != indexed.Hash {
		return nil, "", ErrThumbnailUnavailable
	}
	return l.thumbnailFor(ctx, current, size, background)
}

func (l *Library) thumbnailFor(ctx context.Context, f catalog.File, size int, background bool) ([]byte, string, error) {
	ctx, release := l.previewContext(ctx)
	defer release()
	if size < 96 {
		size = 96
	}
	if size > 1280 {
		size = 1280
	}
	if f.Folder || f.Size <= 0 || f.Size > thumbnailMaxInput {
		return nil, "", ErrThumbnailUnavailable
	}
	key, err := thumbnailKey(l.vault, f, size)
	if err != nil {
		return nil, "", err
	}
	if body, contentType, ok := l.readThumbnailCache(key); ok {
		if err := l.validatePreview(ctx, f); err != nil {
			return nil, "", err
		}
		return body, contentType, nil
	}

	body, mime, err := l.coalescedPreview(ctx, key, func(work context.Context) ([]byte, string, error) {
		return l.generateThumbnail(work, f, size, key, background)
	})
	if accessErr := l.validatePreview(ctx, f); accessErr != nil {
		return nil, "", accessErr
	}
	return body, mime, err
}

func (l *Library) generateThumbnail(ctx context.Context, f catalog.File, size int, key string, background bool) ([]byte, string, error) {
	job := &thumbnailJob{}
	var err error

	if background {
		select {
		case thumbnailBackfill <- struct{}{}:
			defer func() { <-thumbnailBackfill }()
		case <-ctx.Done():
			job.err = ctx.Err()
			return nil, "", job.err
		}
	}
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		job.err = ctx.Err()
		return nil, "", job.err
	}
	var releaseMemory func()
	job.body, job.contentType, releaseMemory, err = l.renderThumbnail(ctx, f, size, background)
	defer releaseMemory()
	if err == nil && f.UserRotation != 0 {
		job.body, job.contentType, err = rotatePreview(ctx, job.body, job.contentType, f.UserRotation)
	}
	if err == nil {
		l.mu.Lock()
		err = l.validatePreviewLocked(ctx, f)
		if err == nil {
			err = l.writeThumbnailCache(key, thumbnailEnvelope{ContentType: job.contentType, Body: job.body})
		}
		l.mu.Unlock()
	}
	if !background && errors.Is(err, ErrPreviewCacheSkipped) {
		err = nil
	}
	if accessErr := l.validatePreview(ctx, f); accessErr != nil {
		err = accessErr
	}
	if err != nil {
		job.body, job.contentType = nil, ""
	}
	job.err = err
	return job.body, job.contentType, job.err
}

func thumbnailKey(v *vault.Vault, f catalog.File, size int) (string, error) {
	identity := f.Hash
	if identity == "" {
		identity = f.EntryID + ":" + strconv.FormatUint(f.Revision, 10)
	}
	if photoPreviewKind(f.Path) == "raster" && f.Orientation > 1 {
		identity += "\x00exif-v1:" + strconv.Itoa(f.Orientation)
	}
	if f.UserRotation != 0 {
		identity += "\x00rotation:" + strconv.Itoa(f.UserRotation)
	}
	key, err := v.Fingerprint("thumbnail", []byte(thumbnailRenderer+"\x00"+strconv.Itoa(size)+"\x00"+identity))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}
