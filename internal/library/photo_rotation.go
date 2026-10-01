package library

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
)

// Rotate only the bounded derivative, never the stored original. It remains
// within the rendering reservation and drops embedded source metadata.
func rotatePreview(ctx context.Context, body []byte, mime string, degrees int) ([]byte, string, error) {
	orientation := 1
	switch degrees {
	case 90:
		orientation = 6
	case 180:
		orientation = 3
	case 270:
		orientation = 8
	case 0:
		return body, mime, nil
	default:
		return nil, "", ErrThumbnailUnavailable
	}
	return orientPreview(ctx, body, mime, orientation)
}

func orientPreview(ctx context.Context, body []byte, mime string, orientation int) ([]byte, string, error) {
	if orientation <= 1 || orientation > 8 {
		return body, mime, nil
	}
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w > 1280 || h > 1280 || w < 1 || h < 1 {
		return nil, "", ErrPreviewTooLarge
	}
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	var out bytes.Buffer
	if mime == "image/jpeg" {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82})
	} else {
		mime = "image/png"
		err = png.Encode(&out, dst)
	}
	if err != nil {
		return nil, "", err
	}
	return out.Bytes(), mime, nil
}
