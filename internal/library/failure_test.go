package library

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestFailedFinalizeLeavesCommittedBytesAndDurableStage(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("failure-pass"), []byte("failure-pass")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	original := []byte("committed-before-failure")
	if _, err := lib.Put(ctx, "disk-image.iso", original); err != nil {
		t.Fatal(err)
	}

	failBinary := filepath.Join(dir, "restic-fail")
	if err := os.WriteFile(failBinary, []byte("#!/bin/sh\necho injected backup failure >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lib.restic = restic.Runner{Binary: failBinary}
	if _, err := lib.Put(ctx, "disk-image.iso", []byte("replacement-that-must-not-win")); err == nil {
		t.Fatal("injected finalize failure unexpectedly succeeded")
	}

	lib.restic = restic.New()
	got, err := lib.Get(ctx, "disk-image.iso")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("failed replacement changed committed bytes: %q", got)
	}
	entries, err := os.ReadDir(lib.stageDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("failed finalize did not leave a durable recovery stage")
	}
}
