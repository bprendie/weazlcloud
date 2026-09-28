package library

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
)

func makeThumbnail(data []byte, size int) ([]byte, string, error) {
	return makeThumbnailContext(context.Background(), data, size)
}

func makeThumbnailContext(ctx context.Context, data []byte, size int) ([]byte, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validThumbnailConfig(config) {
		return nil, "", ErrPreviewTooLarge
	}
	need := int64(config.Width)*int64(config.Height)*8 + int64(len(data))*2 + int64(size*size*8) + 32<<20
	release, err := previewMemory.acquire(ctx, need)
	if err != nil {
		return nil, "", err
	}
	defer release()
	return renderThumbnailBytes(ctx, data, size)
}

func validThumbnailConfig(config image.Config) bool {
	return config.Width > 0 && config.Height > 0 && config.Width <= 20_000 && config.Height <= 20_000 && int64(config.Width)*int64(config.Height) <= thumbnailMaxPixels
}

func renderThumbnailGo(ctx context.Context, data []byte, size int) ([]byte, string, error) {
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
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		for x := 0; x < dw; x++ {
			sx := bounds.Min.X + x*w/dw
			sy := bounds.Min.Y + y*h/dh
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	var out bytes.Buffer
	contentType := "image/png"
	if format == "jpeg" {
		contentType = "image/jpeg"
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}); err != nil {
			return nil, "", err
		}
	} else if err := png.Encode(&out, dst); err != nil {
		return nil, "", err
	}
	return out.Bytes(), contentType, nil
}
