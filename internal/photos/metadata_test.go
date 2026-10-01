package photos

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapturePrecedenceAndTakeoutSidecar(t *testing.T) {
	takeout, err := ParseTakeoutSidecar([]byte("{\"photoTakenTime\":{\"timestamp\":\"1672531200\"}}"))
	if err != nil {
		t.Fatal(err)
	}
	if !takeout.Time.Equal(time.Unix(1672531200, 0).UTC()) || takeout.Source != "takeout-photoTakenTime" {
		t.Fatalf("takeout=%+v", takeout)
	}
	embedded := takeout
	embedded.Time = embedded.Time.Add(24 * time.Hour)
	embedded.Source = "exif-DateTimeOriginal"
	user := embedded
	user.Time = user.Time.Add(24 * time.Hour)
	got, ok := Resolve(Candidates{User: &user, Takeout: &takeout, Embedded: &embedded})
	if !ok || !got.UserCorrected || !got.Time.Equal(user.Time) {
		t.Fatalf("resolved=%+v found=%v", got, ok)
	}
}

func TestParseMinimalJPEGExifDateAndOffset(t *testing.T) {
	raw := minimalJPEGExif(t, "2020:01:02 03:04:05", "+05:30")
	got, err := ParseEmbedded("photo.jpg", raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "exif-DateTimeOriginal" || got.OffsetMinutes == nil || *got.OffsetMinutes != 330 {
		t.Fatalf("capture=%+v", got)
	}
	want := time.Date(2020, 1, 1, 21, 34, 5, 0, time.UTC)
	if !got.Time.Equal(want) {
		t.Fatalf("got=%s want=%s", got.Time, want)
	}
}

func TestMigrationCheckpointIsResumableAndDryRunDoesNotWrite(t *testing.T) {
	checkpointPath := filepath.Join(t.TempDir(), "state.json")
	assets := []Asset{{ID: "a", Revision: 1}, {ID: "b", Revision: 1}}
	applied := 0
	resolve := func(context.Context, Asset) (Capture, bool, error) {
		return Capture{Time: time.Unix(1, 0).UTC(), Source: "test"}, true, nil
	}
	report, err := RunMigration(context.Background(), assets, MigrationOptions{CheckpointPath: checkpointPath, DryRun: true}, resolve, func(Asset, Capture) error {
		applied++
		return nil
	})
	if err != nil || report.Updated != 2 || applied != 0 {
		t.Fatalf("dry run report=%+v applied=%d err=%v", report, applied, err)
	}
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote checkpoint: %v", err)
	}
	report, err = RunMigration(context.Background(), assets, MigrationOptions{CheckpointPath: checkpointPath}, resolve, func(Asset, Capture) error {
		applied++
		return nil
	})
	if err != nil || report.Updated != 2 || applied != 2 {
		t.Fatalf("first report=%+v applied=%d err=%v", report, applied, err)
	}
	report, err = RunMigration(context.Background(), assets, MigrationOptions{CheckpointPath: checkpointPath}, resolve, func(Asset, Capture) error {
		applied++
		return nil
	})
	if err != nil || report.Skipped != 2 || applied != 2 {
		t.Fatalf("resume report=%+v applied=%d err=%v", report, applied, err)
	}
}

func minimalJPEGExif(t *testing.T, date, offset string) []byte {
	t.Helper()
	tiff := make([]byte, 0, 128)
	tiff = append(tiff, 'I', 'I', 42, 0, 8, 0, 0, 0)
	tiff = append(tiff, 2, 0)
	dateBytes := append([]byte(date), 0)
	offsetBytes := append([]byte(offset), 0)
	appendEntry := func(tag, typ uint16, count uint32, value []byte) {
		entry := make([]byte, 12)
		binary.LittleEndian.PutUint16(entry, tag)
		binary.LittleEndian.PutUint16(entry[2:], typ)
		binary.LittleEndian.PutUint32(entry[4:], count)
		if len(value) <= 4 {
			copy(entry[8:], value)
		} else {
			location := uint32(38)
			if tag == 0x9011 {
				location += uint32(len(dateBytes))
			}
			binary.LittleEndian.PutUint32(entry[8:], location)
		}
		tiff = append(tiff, entry...)
	}
	appendEntry(0x0132, 2, uint32(len(dateBytes)), dateBytes)
	appendEntry(0x9011, 2, uint32(len(offsetBytes)), offsetBytes)
	tiff = append(tiff, 0, 0, 0, 0)
	tiff = append(tiff, dateBytes...)
	tiff = append(tiff, offsetBytes...)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	result := []byte{0xff, 0xd8, 0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2), 0}
	result[6] = 0
	result = append(result[:6], segment...)
	return append(result, 0xff, 0xd9)
}
