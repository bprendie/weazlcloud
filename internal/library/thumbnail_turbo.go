package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
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

func validatePhotoWorkerSocket() error {
	socket := strings.TrimSpace(os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET"))
	if socket == "" {
		return nil
	}
	if err := previewrpc.Ready(socket); err != nil {
		return fmt.Errorf("configured photo worker is unavailable: %w", err)
	}
	return nil
}

// ValidatePhotoRenderer checks the in-process renderer used by the private
// worker service. It does not open listeners or read vault data.
func ValidatePhotoRenderer() error {
	if err := validatePreviewRenderer(); err != nil {
		return err
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("required photo media renderer unavailable")
	}
	return nil
}

// RenderPhotoPreview is the bounded, vault-agnostic rendering entry point for
// the local Unix-socket worker. Callers pass only already-authorized bytes.
func RenderPhotoPreview(ctx context.Context, data []byte, size int, media string) ([]byte, string, error) {
	if size < 96 || size > 1280 || len(data) == 0 || len(data) > previewrpc.InputLimit(media) {
		return nil, "", ErrPreviewTooLarge
	}
	if media == "heif" || media == "video" || media == "native" {
		need := int64(len(data))*3 + 256<<20
		if media == "heif" {
			need = max(need, int64(256<<20))
		}
		release, err := previewMemory.acquire(ctx, need)
		if err != nil {
			return nil, "", err
		}
		defer release()
		if media == "heif" {
			jpeg, err := convertHEIFPipe(ctx, data)
			if err != nil {
				return nil, "", fmt.Errorf("HEIF conversion: %w", err)
			}
			return renderFFmpegJPEGPreview(ctx, jpeg, size)
		}
		return renderFFmpegPhotoPreview(ctx, data, size, media)
	}
	if media != "raster" {
		return nil, "", ErrThumbnailUnavailable
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validThumbnailConfig(config) {
		return nil, "", ErrThumbnailUnavailable
	}
	need := int64(len(data))*2 + int64(config.Width)*int64(config.Height)*8 + int64(size*size)*8 + 32<<20
	// Keep the decoded-pixel reservation for native JPEGs too: progressive
	// sources retain large coefficient arrays even when scaled IDCT is small.
	release, err := previewMemory.acquire(ctx, need)
	if err != nil {
		return nil, "", err
	}
	defer release()
	return renderThumbnailBytes(ctx, data, size)
}

func renderFFmpegPhotoPreview(ctx context.Context, data []byte, size int, media string) ([]byte, string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, "", ErrThumbnailUnavailable
	}
	work, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	input, files, cleanup, err := previewMediaInput(data, media)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", size, size)
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1"}, input...)
	args = append(args, "-map", "0:v:0", "-frames:v", "1",
		"-vf", scale, "-an", "-sn", "-dn", "-map_metadata", "-1", "-threads", "1",
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	cmd := exec.CommandContext(work, path, args...)
	cmd.ExtraFiles = files
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	output := &previewOutput{limit: thumbnailMaxOutput}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || work.Err() != nil {
		if work.Err() != nil {
			return nil, "", work.Err()
		}
		return nil, "", ErrThumbnailUnavailable
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(output.Bytes()))
	if err != nil || format != "jpeg" || config.Width < 1 || config.Height < 1 || config.Width > size || config.Height > size {
		return nil, "", ErrThumbnailUnavailable
	}
	return output.Bytes(), "image/jpeg", nil
}

func renderFFmpegJPEGPreview(ctx context.Context, data []byte, size int) ([]byte, string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, "", ErrThumbnailUnavailable
	}
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2", size, size)
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-i", "pipe:0", "-frames:v", "1", "-vf", scale, "-an", "-sn", "-dn", "-map_metadata", "-1", "-threads", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	output := &previewOutput{limit: thumbnailMaxOutput}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, "", ErrThumbnailUnavailable
	}
	return validateJPEGPreview(output.Bytes(), size)
}

func validateJPEGPreview(data []byte, size int) ([]byte, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width < 1 || config.Height < 1 || config.Width > size || config.Height > size {
		return nil, "", ErrThumbnailUnavailable
	}
	return data, "image/jpeg", nil
}

// PhotoWorkerSlots returns the worker process concurrency permitted by its own
// cgroup/affinity allocation. The container quota is the aggregate CPU limit.
func PhotoWorkerSlots() int {
	resources := discoverPreviewResources()
	return max(1, int(math.Floor(resources.CPUs)))
}

func renderPhotoPreview(ctx context.Context, data []byte, size int, media string) ([]byte, string, error) {
	socket := strings.TrimSpace(os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET"))
	if socket == "" {
		if media == "raster" {
			return renderThumbnailBytes(ctx, data, size)
		}
		return renderFFmpegPhotoPreview(ctx, data, size, media)
	}
	return previewrpc.Render(ctx, socket, data, size, media)
}

func renderThumbnailBytes(ctx context.Context, data []byte, size int) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(data) > previewrpc.InputLimit("raster") || size < 96 || size > 1280 {
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
	var body []byte
	var mime string
	if path == "" || format != "jpeg" || cfg.ColorModel == color.CMYKModel {
		body, mime, err = renderThumbnailGo(ctx, data, size)
	} else {
		body, mime, err = renderThumbnailTurbo(ctx, path, data, size, cfg)
	}
	if err != nil {
		return nil, "", err
	}
	if format == "jpeg" {
		if metadata, parseErr := photos.ParseMediaMetadata("photo.jpg", data); parseErr == nil && metadata.Orientation > 1 {
			return orientPreview(ctx, body, mime, metadata.Orientation)
		}
	}
	return body, mime, nil
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
