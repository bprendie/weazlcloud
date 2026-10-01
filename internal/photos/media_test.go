package photos

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestParseMediaMetadataReadsRasterDimensions(t *testing.T) {
	imageData := image.NewRGBA(image.Rect(0, 0, 19, 11))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	got, err := ParseMediaMetadata("photo.png", encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 19 || got.Height != 11 || got.Orientation != 1 {
		t.Fatalf("metadata=%+v", got)
	}
}

func TestParseJPEGOrientation(t *testing.T) {
	tiff := make([]byte, 8+2+12+4)
	copy(tiff, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(tiff[8:], 1)
	binary.LittleEndian.PutUint16(tiff[10:], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:], 3)
	binary.LittleEndian.PutUint32(tiff[14:], 1)
	binary.LittleEndian.PutUint16(tiff[18:], 6)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	raw := []byte{0xff, 0xd8, 0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2)}
	raw = append(raw, segment...)
	raw = append(raw, 0xff, 0xd9)
	got, err := ParseMediaMetadata("photo.jpg", raw)
	if err == nil {
		t.Fatalf("expected invalid raster payload, got metadata=%+v", got)
	}
	if orientation := parseJPEGOrientation(raw); orientation != 6 {
		t.Fatalf("orientation=%d", orientation)
	}
}
