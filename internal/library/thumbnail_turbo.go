package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const turboHelper = "weazl-preview-turbo"

func previewRenderer() (string, string, error) {
	mode := os.Getenv("WEAZLCLOUD_PREVIEW_RENDERER")
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "go" && mode != "turbo" {
		return "", "", errors.New("WEAZLCLOUD_PREVIEW_RENDERER must be auto, go or turbo")
	}
	if mode == "go" {
		return mode, "", nil
	}
	path, err := exec.LookPath(turboHelper)
	if err != nil && mode == "turbo" {
		return "", "", fmt.Errorf("required preview helper unavailable: %w", err)
	}
	return mode, path, nil
}

func validatePreviewRenderer() error {
	_, path, err := previewRenderer()
	if err != nil || path == "" {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out := &previewOutput{limit: 128}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil || string(out.Bytes()) != "weazl-preview-turbo-v1\n" {
		return errors.New("preview helper version check failed")
	}
	return nil
}

func renderThumbnailBytes(ctx context.Context, data []byte, size int) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(data) > thumbnailMaxInput || size < 96 || size > 1280 {
		return nil, "", ErrPreviewTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrThumbnailUnavailable
	}
	if !validThumbnailConfig(cfg) {
		return nil, "", ErrPreviewTooLarge
	}
	_, path, err := previewRenderer()
	if err != nil {
		return nil, "", err
	}
	// CMYK uses the existing Go conversion. PNG/GIF preserve alpha and first-frame behavior.
	if path == "" || format != "jpeg" || cfg.ColorModel == color.CMYKModel {
		return renderThumbnailGo(ctx, data, size)
	}
	return renderThumbnailTurbo(ctx, path, data, size, cfg)
}

func renderThumbnailTurbo(ctx context.Context, path string, data []byte, size int, source image.Config) ([]byte, string, error) {
	work, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(work, path, strconv.Itoa(size))
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	out := &previewOutput{limit: thumbnailMaxOutput}
	cmd.Stdout = out
	// Decoder errors are deliberately generic: no source metadata in logs.
	if err := cmd.Run(); err != nil {
		if work.Err() != nil {
			return nil, "", work.Err()
		}
		return nil, "", ErrThumbnailUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Bytes()))
	w, h := size, size
	if source.Width > source.Height {
		h = max(1, size*source.Height/source.Width)
	} else {
		w = max(1, size*source.Width/source.Height)
	}
	if err != nil || format != "jpeg" || cfg.Width != w || cfg.Height != h {
		return nil, "", ErrThumbnailUnavailable
	}
	return out.Bytes(), "image/jpeg", nil
}

type previewOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (w *previewOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, io.ErrShortBuffer
	}
	return w.buffer.Write(p)
}

func (w *previewOutput) Bytes() []byte { return w.buffer.Bytes() }
func (w *previewOutput) Len() int      { return w.buffer.Len() }
