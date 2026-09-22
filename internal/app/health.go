package app

import (
	"fmt"
	"os"
)

// storageReady proves that both configured data and process temporary storage
// can accept a durable probe. A path existing is not enough for a usable node:
// read-only mounts and exhausted filesystems must fail readiness.
func storageReady(dataDir string) error {
	if err := probeDirectory(dataDir, "data"); err != nil {
		return err
	}
	if err := probeDirectory(os.TempDir(), "temporary"); err != nil {
		return err
	}
	return nil
}

func probeDirectory(dir, label string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s storage: %w", label, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s storage is not a directory", label)
	}
	probe, err := os.CreateTemp(dir, ".weazl-ready-*")
	if err != nil {
		return fmt.Errorf("%s storage is not writable: %w", label, err)
	}
	name := probe.Name()
	defer os.Remove(name)
	if _, err := probe.Write([]byte("ok")); err != nil {
		_ = probe.Close()
		return fmt.Errorf("%s storage probe write: %w", label, err)
	}
	if err := probe.Sync(); err != nil {
		_ = probe.Close()
		return fmt.Errorf("%s storage probe sync: %w", label, err)
	}
	if err := probe.Close(); err != nil {
		return fmt.Errorf("%s storage probe close: %w", label, err)
	}
	return nil
}
