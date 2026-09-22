package desk

import (
	"net/http"
	"testing"
)

func TestCapabilityUsesContentWhenExtensionIsWrong(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	capability := capabilityFor("notes.txt", int64(len(png)), png)
	if capability.Kind != "thumbnail" || capability.ContentType != "image/png" {
		t.Fatalf("wrong-extension PNG capability = %+v", capability)
	}
}

func TestCapabilityRecognizesExtensionlessText(t *testing.T) {
	capability := capabilityFor("README", 18, []byte("plain text from disk\n"))
	if capability.Kind != "text" || capability.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("extensionless text capability = %+v", capability)
	}
}

func TestCapabilityKeepsUnsupportedLargeBinaryAsDownload(t *testing.T) {
	capability := capabilityFor("disk.img", 8<<30, []byte{0, 1, 2, 3})
	if capability.Kind != "download" {
		t.Fatalf("binary capability = %+v", capability)
	}
	if http.DetectContentType([]byte{0, 1, 2, 3}) == "" {
		t.Fatal("content detector returned no type")
	}
}
