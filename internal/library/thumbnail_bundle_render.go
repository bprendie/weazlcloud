package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"go.n16f.net/thumbhash"
)

// RenderPhotoBundle is the vault-agnostic worker entry. The API admits its own
// source buffers separately; the worker admits decoded pixels and encoders here.
func RenderPhotoBundle(ctx context.Context, data []byte, sizes []int, media string, emit func(previewrpc.Variant) error) error {
	if !previewrpc.BundleSizes(sizes) || len(data) == 0 || len(data) > thumbnailMaxInput {
		return ErrPreviewTooLarge
	}
	need := int64(len(data))*2 + 256<<20
	if media == "raster" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || !validThumbnailConfig(cfg) {
			return ErrThumbnailUnavailable
		}
		need = int64(len(data))*2 + int64(cfg.Width)*int64(cfg.Height)*8 + 64<<20
	}
	release, err := previewMemory.acquire(ctx, need)
	if err != nil {
		return err
	}
	defer release()
	return renderBundleBytes(ctx, data, sizes, media, emit)
}

func renderPhotoBundle(ctx context.Context, data []byte, sizes []int, media string, emit func(previewrpc.Variant) error) error {
	if socket := strings.TrimSpace(os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET")); socket != "" {
		return previewrpc.RenderBundle(ctx, socket, data, sizes, media, emit)
	}
	return renderBundleBytes(ctx, data, sizes, media, emit)
}

func renderBundleBytes(ctx context.Context, data []byte, sizes []int, media string, emit func(previewrpc.Variant) error) error {
	if !previewrpc.BundleSizes(sizes) || len(data) == 0 || len(data) > thumbnailMaxInput {
		return ErrPreviewTooLarge
	}
	sizes = append([]int(nil), sizes...)
	sort.Ints(sizes)
	if media != "raster" {
		var err error
		if media == "heif" {
			data, err = convertHEIFPipe(ctx, data)
		} else if media == "video" || media == "native" {
			data, _, err = renderFFmpegPhotoPreview(ctx, data, sizes[len(sizes)-1], media)
		} else {
			return ErrThumbnailUnavailable
		}
		if err != nil {
			return err
		}
		defer clear(data)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validThumbnailConfig(cfg) {
		return ErrThumbnailUnavailable
	}
	_, helper, err := previewRenderer()
	if err != nil {
		return err
	}
	orientation := 1
	if format == "jpeg" {
		if metadata, err := photos.ParseMediaMetadata("photo.jpg", data); err == nil {
			orientation = metadata.Orientation
		}
	}
	var hash []byte
	total := 0
	output := func(v previewrpc.Variant) error {
		total += len(v.Body) + 7
		if total > previewrpc.MaxBundleOutput-1024 {
			return ErrPreviewTooLarge
		}
		if orientation > 1 {
			var err error
			v.Body, v.MIME, err = orientPreview(ctx, v.Body, v.MIME, orientation)
			if err != nil {
				return err
			}
		}
		if hash == nil {
			hash = previewThumbHash(v.Body)
		}
		v.ThumbHash = hash
		return emit(v)
	}
	if helper != "" && format == "jpeg" && cfg.ColorModel != color.CMYKModel {
		if supportsPreviewBundle(ctx, helper) {
			return renderTurboBundle(ctx, helper, data, sizes, output)
		}
		for _, size := range sizes {
			body, mime, err := renderThumbnailTurbo(ctx, helper, data, size, cfg)
			if err != nil {
				return err
			}
			if err = output(previewrpc.Variant{Size: size, Body: body, MIME: mime}); err != nil {
				return err
			}
		}
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	for _, size := range sizes {
		body, mime, err := resizeDecodedPreview(ctx, src, format, size)
		if err != nil {
			return err
		}
		if err = output(previewrpc.Variant{Size: size, Body: body, MIME: mime}); err != nil {
			return err
		}
	}
	return nil
}

func resizeDecodedPreview(ctx context.Context, src image.Image, format string, size int) ([]byte, string, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := size, size
	if w > h {
		dh = max(1, size*h/w)
	} else {
		dw = max(1, size*w/h)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		for x := 0; x < dw; x++ {
			dst.Set(x, y, src.At(b.Min.X+x*w/dw, b.Min.Y+y*h/dh))
		}
	}
	var out bytes.Buffer
	mime := "image/png"
	var err error
	if format == "jpeg" {
		mime = "image/jpeg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82})
	} else {
		err = png.Encode(&out, dst)
	}
	if err != nil || out.Len() > thumbnailMaxOutput {
		return nil, "", ErrThumbnailUnavailable
	}
	return out.Bytes(), mime, nil
}

func previewThumbHash(body []byte) []byte {
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	return thumbhash.EncodeImage(src)
}

func renderTurboBundle(ctx context.Context, helper string, data []byte, sizes []int, emit func(previewrpc.Variant) error) error {
	work, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	values := make([]string, len(sizes))
	for i, size := range sizes {
		values[i] = strconv.Itoa(size)
	}
	cmd := exec.CommandContext(work, helper, "--bundle", strings.Join(values, ","))
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	readErr := previewrpc.ReadBundle(output, sizes, emit)
	if readErr != nil {
		cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil || err != nil {
		return ErrThumbnailUnavailable
	}
	return nil
}
