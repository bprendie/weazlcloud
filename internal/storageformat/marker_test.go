package storageformat

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeWritesPrivateLegacyMarkerAndCheckIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	if err := Check(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only check created marker: %v", err)
	}
	if err := Initialize(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, MarkerName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker permissions=%o", info.Mode().Perm())
	}
	if err := Check(dir); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedMarkerFailsWithoutRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, MarkerName)
	original := []byte(`{"format_version":2,"minimum_reader_version":2,"minimum_writer_version":2,"mode":"shared"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("check error=%v", err)
	}
	if err := Initialize(dir); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("initialize error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("unsupported marker was rewritten")
	}
}

func TestMalformedMarkerFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir); err == nil {
		t.Fatal("malformed marker was accepted")
	}
}
