package catalog

import (
	"testing"
	"time"
)

func TestCaptureMetadataRevisionAndManualPrecedence(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/photo.jpg", Size: 1, Mtime: time.Now().UTC(), Present: true}); err != nil {
		t.Fatal(err)
	}
	first, ok := c.Get("Photos/photo.jpg")
	if !ok || first.ImportedAt.IsZero() {
		t.Fatalf("missing imported timestamp: %+v", first)
	}
	capture := time.Date(2013, 4, 5, 6, 7, 8, 0, time.UTC)
	offset := -240
	updated, err := c.UpdateCapture(first.EntryID, first.Revision, CaptureMetadata{Time: &capture, OffsetMinutes: &offset, Source: "takeout-photoTakenTime"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != first.Revision+1 || updated.CaptureTime == nil || !updated.CaptureTime.Equal(capture) {
		t.Fatalf("updated=%+v", updated)
	}
	manual := capture.Add(24 * time.Hour)
	manualUpdated, err := c.UpdateCapture(updated.EntryID, updated.Revision, CaptureMetadata{Time: &manual, Source: "user", UserCorrected: true})
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := c.UpdateCapture(manualUpdated.EntryID, manualUpdated.Revision, CaptureMetadata{Time: &capture, Source: "exif-DateTimeOriginal"})
	if err != nil {
		t.Fatal(err)
	}
	if ignored.Revision != manualUpdated.Revision || !ignored.CaptureTime.Equal(manual) || !ignored.CaptureUserCorrected {
		t.Fatalf("manual correction was overwritten: %+v", ignored)
	}
	reloaded := New(c.path, c.vault)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Get("Photos/photo.jpg")
	if !ok || persisted.CaptureTime == nil || !persisted.CaptureTime.Equal(manual) || persisted.ImportedAt.IsZero() {
		t.Fatalf("metadata did not persist: %+v", persisted)
	}
}

func TestPhotoMetadataAppliesAtomicallyAndKeepsUserFields(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/photo.png", Size: 1, Mtime: time.Now().UTC(), Present: true, Favorite: true, Caption: "keep"}); err != nil {
		t.Fatal(err)
	}
	file, ok := c.Get("Photos/photo.png")
	if !ok {
		t.Fatal("photo was not stored")
	}
	capture := time.Date(2013, 4, 5, 6, 7, 8, 0, time.UTC)
	offset := -240
	updated, err := c.UpdatePhotoMetadata(file.EntryID, file.Revision,
		&CaptureMetadata{Time: &capture, OffsetMinutes: &offset, Source: "takeout-photoTakenTime"},
		&MediaMetadata{Width: 1920, Height: 1080, Orientation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != file.Revision+1 || updated.Width != 1920 || updated.Height != 1080 || updated.CaptureTime == nil || !updated.CaptureTime.Equal(capture) || !updated.Favorite || updated.Caption != "keep" {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestCaptureMetadataRejectsInvalidDatesAndOffsets(t *testing.T) {
	c := testCatalog(t)
	if err := c.Put(File{Path: "Photos/photo.jpg", Size: 1, Mtime: time.Now().UTC(), Present: true}); err != nil {
		t.Fatal(err)
	}
	file, _ := c.Get("Photos/photo.jpg")
	invalidDate := time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.UpdateCapture(file.EntryID, file.Revision, CaptureMetadata{Time: &invalidDate}); err != ErrInvalidCaptureMetadata {
		t.Fatalf("invalid date error=%v", err)
	}
	offset := 25 * 60
	if _, err := c.UpdateCapture(file.EntryID, file.Revision, CaptureMetadata{OffsetMinutes: &offset}); err != ErrInvalidCaptureMetadata {
		t.Fatalf("invalid offset error=%v", err)
	}
}
