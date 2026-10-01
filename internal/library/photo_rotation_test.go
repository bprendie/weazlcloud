package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestPhotoRotationChangesDerivativeAndPreservesOriginalAndFavorite(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	src := image.NewRGBA(image.Rect(0, 0, 20, 10))
	src.Set(0, 0, color.RGBA{R: 255, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, src); err != nil {
		t.Fatal(err)
	}
	file, err := l.Put(ctx, "Photos/rotate.png", raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.catalog.Get(file.Path)
	originalKey, err := thumbnailKey(l.vault, file, 96)
	if err != nil {
		t.Fatal(err)
	}
	rotation := 90
	item, err := l.UpdatePhoto(ctx, file.EntryID, PhotoUpdate{Rotation: &rotation}, false)
	if err != nil || item.UserRotation != 90 {
		t.Fatalf("rotation=%+v error=%v", item, err)
	}
	rotated, _ := l.catalog.Get(file.Path)
	newKey, err := thumbnailKey(l.vault, rotated, 96)
	if err != nil || newKey == originalKey {
		t.Fatalf("rotation cache key unchanged: %v", err)
	}
	body, _, err := l.PhotoThumbnail(ctx, file.EntryID, 96)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width != 48 || cfg.Height != 96 {
		t.Fatalf("rotated bounds=%+v error=%v", cfg, err)
	}
	favorite, err := l.SetPhotoFavorite(ctx, file.EntryID, true)
	if err != nil || favorite.UserRotation != 90 {
		t.Fatalf("favorite lost rotation: %+v error=%v", favorite, err)
	}
	var original bytes.Buffer
	if err := l.StreamTo(ctx, file.Path, &original); err != nil || !bytes.Equal(original.Bytes(), raw.Bytes()) {
		t.Fatalf("original changed: %v", err)
	}
}

func TestDerivativeEXIFOrientationsPreserveCornerPixels(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	red := color.RGBA{R: 255, A: 255}
	src.Set(0, 0, red)
	var raw bytes.Buffer
	_ = png.Encode(&raw, src)
	for _, tc := range []struct{ orientation, x, y, w, h int }{{2, 2, 0, 3, 2}, {3, 2, 1, 3, 2}, {4, 0, 1, 3, 2}, {5, 0, 0, 2, 3}, {6, 1, 0, 2, 3}, {7, 1, 2, 2, 3}, {8, 0, 2, 2, 3}} {
		body, _, err := orientPreview(context.Background(), raw.Bytes(), "image/png", tc.orientation)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != tc.w || img.Bounds().Dy() != tc.h || color.RGBAModel.Convert(img.At(tc.x, tc.y)) != red {
			t.Fatalf("orientation %d has incorrect geometry/pixels", tc.orientation)
		}
	}
}
