package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"
	"time"
)

func TestBackfillPhotoMetadataUsesSidecarDateAndCheckpoint(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.Put(ctx, "Photos/Trip/photo.jpg", []byte("jpeg placeholder")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/Trip/photo.jpg.json", []byte("{\"photoTakenTime\":{\"timestamp\":\"1365152400\"}}")); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(t.TempDir(), "photo-metadata.json")
	report, err := l.BackfillPhotoMetadata(ctx, PhotoMetadataOptions{Root: "Photos", CheckpointPath: checkpoint, SidecarsOnly: true})
	if err != nil || report.Updated != 1 || report.Errors != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	file, err := l.Metadata(ctx, "Photos/Trip/photo.jpg")
	if err != nil || file.CaptureTime == nil || file.CaptureSource != "takeout-photoTakenTime" {
		t.Fatalf("file=%+v err=%v", file, err)
	}
	if !file.CaptureTime.Equal(time.Unix(1365152400, 0).UTC()) || file.ImportedAt.IsZero() {
		t.Fatalf("capture/import metadata=%+v", file)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].CapturedAt == nil || !page.Items[0].CapturedAt.Equal(*file.CaptureTime) {
		t.Fatalf("photo page=%+v err=%v", page, err)
	}
}

func TestBackfillPhotoMetadataStoresRasterMediaFields(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	imageData := image.NewRGBA(image.Rect(0, 0, 13, 7))
	imageData.Set(0, 0, color.RGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(ctx, "Photos/Trip/photo.png", encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	report, err := l.BackfillPhotoMetadata(ctx, PhotoMetadataOptions{Root: "Photos", CheckpointPath: filepath.Join(t.TempDir(), "media.json")})
	if err != nil || report.Updated != 1 || report.Errors != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	file, err := l.Metadata(ctx, "Photos/Trip/photo.png")
	if err != nil || file.Width != 13 || file.Height != 7 || file.Orientation != 1 {
		t.Fatalf("file=%+v err=%v", file, err)
	}
	page, err := l.PhotoPage(ctx, 10, "", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Width != 13 || page.Items[0].Height != 7 {
		t.Fatalf("photo page=%+v err=%v", page, err)
	}
}
