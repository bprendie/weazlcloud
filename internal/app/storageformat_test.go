package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/config"
	"github.com/bprendie/weazlcloud/internal/storageformat"
)

func TestStartRejectsUnsupportedStorageBeforeBindingListeners(t *testing.T) {
	dir := t.TempDir()
	marker := []byte(`{"format_version":9,"minimum_reader_version":9,"minimum_writer_version":9,"mode":"future"}`)
	if err := os.WriteFile(filepath.Join(dir, storageformat.MarkerName), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: dir, DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0"}
	if _, err := Start(cfg); !errors.Is(err, storageformat.ErrUnsupported) {
		t.Fatalf("Start error=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, storageformat.MarkerName))
	if err != nil || !bytes.Equal(got, marker) {
		t.Fatalf("marker changed: err=%v", err)
	}
}

func TestCheckCommandValidatesMarkerWithoutInitializingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	var output bytes.Buffer
	err := Run(context.Background(), []string{"-data", dir, "-check"}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, storageformat.MarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check command wrote format marker: %v", err)
	}
}
