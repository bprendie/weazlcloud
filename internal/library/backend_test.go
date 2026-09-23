package library

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestResticBackendStreamsRangesAndDefersHeldSnapshotPruning(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("backend-test"), []byte("backend-test")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("range-and-hold-"), 1024)
	if _, err := lib.Put(ctx, "held.bin", payload); err != nil {
		t.Fatal(err)
	}
	file, err := lib.Metadata(ctx, "held.bin")
	if err != nil {
		t.Fatal(err)
	}
	var ranged bytes.Buffer
	if err := lib.StreamRange(ctx, "held.bin", 29, 83, &ranged); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ranged.Bytes(), payload[29:112]) {
		t.Fatal("backend returned the wrong byte range")
	}
	ref, err := lib.backend.Capture(file)
	if err != nil {
		t.Fatal(err)
	}
	release, err := lib.backend.Hold(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Delete("held.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CleanupTrash(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshots, err := lib.backend.Snapshots(ctx)
	if err != nil || len(snapshots) != 1 || snapshots[0] != ref.Snapshot {
		t.Fatalf("held snapshot pruned: snapshots=%v err=%v", snapshots, err)
	}
	release()
	if _, err := lib.CleanupTrash(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshots, err = lib.backend.Snapshots(ctx)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("released snapshot not pruned: snapshots=%v err=%v", snapshots, err)
	}
}
