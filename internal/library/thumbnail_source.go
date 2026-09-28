package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

const previewHeaderLimit = 1 << 20

// Include a conservative allowance for the Restic child, not just Go's image
// buffer. This is an admission estimate, not a subprocess RSS limit.
func sourceAllowance(f catalog.File) int64 {
	if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
		return 16 << 20
	}
	return 128 << 20
}

func (l *Library) renderThumbnail(ctx context.Context, f catalog.File, size int, background bool) ([]byte, string, func(), error) {
	noop := func() {}
	acquire := previewMemory.acquire
	if background {
		acquire = previewMemory.acquireBackground
	}
	// Probe a bounded header first. Release its entire reservation before
	// acquiring the complete pipeline budget: no worker upgrades a held budget.
	release, err := acquire(ctx, sourceAllowance(f)+2*previewHeaderLimit)
	if err != nil {
		return nil, "", noop, err
	}
	header, err := l.thumbnailSource(ctx, f, min(f.Size, previewHeaderLimit), true)
	var cfg image.Config
	if err == nil {
		cfg, _, err = image.DecodeConfig(bytes.NewReader(header))
	}
	clear(header)
	header = nil
	release()
	if err != nil {
		return nil, "", noop, ErrThumbnailUnavailable
	}
	if !validThumbnailConfig(cfg) {
		return nil, "", noop, ErrPreviewTooLarge
	}
	need := sourceAllowance(f) + f.Size*2 + int64(cfg.Width)*int64(cfg.Height)*8 + int64(size*size)*8 + 32<<20
	release, err = acquire(ctx, need)
	if err != nil {
		return nil, "", noop, err
	}
	data, err := l.thumbnailSource(ctx, f, f.Size, false)
	if err != nil {
		return nil, "", release, err
	}
	defer clear(data)
	// Verify again before decoding; a backend must not substitute a larger image.
	actual, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || actual.Width != cfg.Width || actual.Height != cfg.Height {
		return nil, "", release, ErrThumbnailUnavailable
	}
	body, mime, err := renderThumbnailBytes(ctx, data, size)
	return body, mime, release, err
}

func (l *Library) thumbnailSource(ctx context.Context, expected catalog.File, limit int64, prefix bool) ([]byte, error) {
	l.mu.Lock()
	err := l.validatePreviewLocked(ctx, expected)
	var ref catalog.Reference
	var release func()
	if err == nil {
		ref, err = l.capture(expected)
	}
	if err == nil {
		release, err = l.holdReference(ref)
	}
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer release()
	select {
	case thumbnailReaders <- struct{}{}:
		defer func() { <-thumbnailReaders }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	readCtx, cancel := context.WithCancel(ctx)
	readCtx = restic.WithMemoryLimit(readCtx, sourceAllowance(expected))
	defer cancel()
	out := &boundedPreviewWriter{limit: int(limit), cancel: cancel}
	out.Grow(int(limit))
	err = l.readReference(readCtx, ref, out)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out.exceeded && prefix {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if !prefix && int64(out.Len()) != expected.Size {
		return nil, io.ErrUnexpectedEOF
	}
	return out.Bytes(), nil
}

type boundedPreviewWriter struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (w *boundedPreviewWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if len(p) <= remaining {
		return w.buffer.Write(p)
	}
	n, _ := w.buffer.Write(p[:remaining])
	w.exceeded = true
	w.cancel()
	return n, errors.New("preview source exceeded its declared size")
}

func (w *boundedPreviewWriter) Grow(n int)    { w.buffer.Grow(n) }
func (w *boundedPreviewWriter) Len() int      { return w.buffer.Len() }
func (w *boundedPreviewWriter) Bytes() []byte { return w.buffer.Bytes() }
