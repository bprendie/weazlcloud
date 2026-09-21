package library

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestMakeThumbnailBoundsRasterWithoutSourceGrowth(t *testing.T) {
	data := make([]byte, 0)
	var source bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	data = source.Bytes()
	body, contentType, err := makeThumbnail(data, 320)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q", contentType)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 160 {
		t.Fatalf("thumbnail bounds = %v", decoded.Bounds())
	}
}
