package library

import (
	"bytes"
	"context"
	"image"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func (l *Library) generateBundle(ctx context.Context, f catalog.File, job *assetPreviewJob) error {
	l.thumbMu.Lock()
	background := job.background
	l.thumbMu.Unlock()
	if background {
		select {
		case thumbnailBackfill <- struct{}{}:
			defer func() { <-thumbnailBackfill }()
		case <-job.promote:
			background = false
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	// A competing request may have persisted a variant between the outer cache
	// lookup and joining this asset job. Satisfy it without restoring the original.
	for _, size := range l.pendingBundle(job, false) {
		key, err := thumbnailKey(l.vault, f, size)
		if err != nil {
			return err
		}
		if body, mime, ok := l.readThumbnailCache(key); ok {
			l.finishBundleVariant(job, size, body, mime, nil)
		}
	}
	if len(l.pendingBundle(job, true)) == 0 {
		return nil
	}
	reportPhotoProgress(ctx, 10)
	source, release, err := l.bundleSource(ctx, f, background)
	if err != nil {
		return err
	}
	defer release()
	defer clear(source)
	reportPhotoProgress(ctx, 80)
	for {
		sizes := l.pendingBundle(job, true)
		if len(sizes) == 0 {
			return nil
		}
		err := renderPhotoBundle(ctx, source, sizes, photoPreviewKind(f.Path), func(v previewrpc.Variant) error {
			cfg, format, err := image.DecodeConfig(bytes.NewReader(v.Body))
			if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > v.Size || cfg.Height > v.Size || ("image/"+format) != v.MIME {
				return ErrThumbnailUnavailable
			}
			if f.UserRotation != 0 {
				v.Body, v.MIME, err = rotatePreview(ctx, v.Body, v.MIME, f.UserRotation)
				if err != nil {
					return err
				}
				v.ThumbHash = previewThumbHash(v.Body)
			}
			key, err := thumbnailKey(l.vault, f, v.Size)
			if err != nil {
				return err
			}
			err = l.validatePreview(ctx, f)
			if err == nil {
				err = l.writeThumbnailCache(key, thumbnailEnvelope{ContentType: v.MIME, Body: v.Body, ThumbHash: v.ThumbHash, Size: v.Size})
			}
			if accessErr := l.validatePreview(ctx, f); accessErr != nil {
				return accessErr
			}
			if err == nil {
				err = l.writePreviewManifest(f, v.ThumbHash)
			}
			l.finishBundleVariant(job, v.Size, v.Body, v.MIME, err)
			reportPhotoProgress(ctx, 95)
			return nil
		})
		if err != nil {
			return err
		}
	}
}

// Unknown dimensions use a conservative reservation before one admitted read.
// On a small host that cannot fit it, retain the bounded two-read header path.
func (l *Library) bundleSource(ctx context.Context, f catalog.File, background bool) ([]byte, func(), error) {
	noop := func() {}
	acquire := previewMemory.acquire
	if background {
		acquire = previewMemory.acquireBackground
	}
	raster := photoPreviewKind(f.Path) == "raster"
	pixels := int64(thumbnailMaxPixels)
	if raster && f.Width > 0 && f.Height > 0 {
		if !validThumbnailConfig(image.Config{Width: f.Width, Height: f.Height}) {
			return nil, noop, ErrPreviewTooLarge
		}
		pixels = int64(f.Width) * int64(f.Height)
	}
	base := sourceAllowance(f) + f.Size*2 + 64<<20
	need := base + pixels*8
	if !raster {
		need = base + 256<<20
	}
	if need > previewMemory.limit && raster && (f.Width == 0 || f.Height == 0) {
		release, err := acquire(ctx, sourceAllowance(f)+2*previewHeaderLimit)
		if err != nil {
			return nil, noop, err
		}
		header, err := l.thumbnailSource(ctx, f, min(f.Size, previewHeaderLimit), true)
		cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(header))
		clear(header)
		release()
		if err != nil || decodeErr != nil || !validThumbnailConfig(cfg) {
			return nil, noop, ErrThumbnailUnavailable
		}
		pixels = int64(cfg.Width) * int64(cfg.Height)
		need = base + pixels*8
	}
	release, err := acquire(ctx, need)
	if err != nil {
		return nil, noop, err
	}
	body, err := l.thumbnailSource(ctx, f, f.Size, false)
	if err == nil && raster {
		cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(body))
		if decodeErr != nil || !validThumbnailConfig(cfg) || int64(cfg.Width)*int64(cfg.Height) > pixels || (f.Width > 0 && f.Height > 0 && (cfg.Width != f.Width || cfg.Height != f.Height)) {
			err = ErrThumbnailUnavailable
		}
	}
	if err != nil {
		clear(body)
		release()
		return nil, noop, err
	}
	return body, release, nil
}
