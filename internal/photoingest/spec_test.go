package photoingest

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestOpaqueNegotiationAndDNG(t *testing.T) {
	spec := Spec{DeviceID: "phone", DeviceAssetID: "asset", Components: []Component{{ID: "original", Filename: "camera.raw", Size: 4, SHA256: strings.Repeat("a", 64)}}}
	if spec.Normalize() == nil {
		t.Fatal("unknown accepted without negotiation")
	}
	spec.OriginalMode = "opaque-original-v1"
	spec.Transport = "parts-v1"
	spec.Hidden = true
	if err := spec.Normalize(); err != nil {
		t.Fatal(err)
	}
	if spec.Components[0].MediaType != "application/octet-stream" || destinationName(spec, spec.Components[0]) != "camera.raw.opaque" {
		t.Fatal(spec)
	}
	reader, err := verifyMedia(bytes.NewReader([]byte("raw!")), spec.Components[0].MediaType)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(reader)
	if string(b) != "raw!" {
		t.Fatal("bytes changed")
	}
	dng := []byte{'I', 'I', 42, 0, 0, 0, 0, 0}
	reader, err = verifyMedia(bytes.NewReader(dng), "image/dng")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(reader)
	if !bytes.Equal(b, dng) {
		t.Fatal("DNG changed")
	}
	if _, err = verifyMedia(bytes.NewReader([]byte("corrupt")), "image/jpeg"); err == nil {
		t.Fatal("known mismatch accepted")
	}
	spec.CapturedAt = "2026-10-02T12:00:00-04:00"
	capture, err := spec.Capture()
	if err != nil || capture.OffsetMinutes != nil {
		t.Fatalf("unknown offset: %+v %v", capture, err)
	}
	spec.OffsetKnown = true
	capture, _ = spec.Capture()
	if *capture.OffsetMinutes != -240 {
		t.Fatal(capture)
	}
}
