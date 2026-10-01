package photos

import (
	"encoding/binary"
	"testing"
)

func TestJPEGCameraAndMalformedExifPointer(t *testing.T) {
	tiff := make([]byte, 64)
	copy(tiff, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(tiff[8:], 2)
	for i, tag := range []uint16{0x010f, 0x0110} {
		entry := tiff[10+i*12:]
		binary.LittleEndian.PutUint16(entry, tag)
		binary.LittleEndian.PutUint16(entry[2:], 2)
		binary.LittleEndian.PutUint32(entry[4:], 5)
		binary.LittleEndian.PutUint32(entry[8:], uint32(40+i*8))
	}
	copy(tiff[40:], "SONY\x00")
	copy(tiff[48:], "A7IV\x00")
	segment := append([]byte("Exif\x00\x00"), tiff...)
	raw := append([]byte{0xff, 0xd8, 0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2)}, segment...)
	raw = append(raw, 0xff, 0xd9)
	if got := parseJPEGCamera(raw); got != "SONY A7IV" {
		t.Fatalf("camera=%q", got)
	}
	reader := exifReader{raw: tiff, order: binary.LittleEndian}
	if _, _, found := (exifIFD{0x8769: []byte{1}}).captureDate(reader); found {
		t.Fatal("corrupt EXIF pointer returned a date")
	}
}
