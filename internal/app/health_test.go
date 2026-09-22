package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageReadyRequiresWritableDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := storageReady(dir); err != nil {
		t.Fatalf("writable directory was not ready: %v", err)
	}

	missing := filepath.Join(dir, "missing")
	if err := storageReady(missing); err == nil {
		t.Fatal("missing data directory reported ready")
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storageReady(file); err == nil {
		t.Fatal("regular file reported as a ready data directory")
	}

	if os.Geteuid() != 0 {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		if err := storageReady(dir); err == nil {
			t.Fatal("read-only data directory reported ready")
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}
