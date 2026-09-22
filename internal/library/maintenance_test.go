package library

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestTrashSnapshotPlanKeepsLiveAndNewerReferences(t *testing.T) {
	cutoff := time.Now().UTC()
	old := cutoff.Add(-time.Second)
	newer := cutoff.Add(time.Second)
	all := []catalog.File{
		{Path: "zero.bin", Size: 0, Snap: "shared", DeletedAt: &cutoff},
		{Path: "live.bin", Size: 4, Snap: "shared", Present: true},
		{Path: "expired.bin", Size: 8, Snap: "orphan", DeletedAt: &old},
		{Path: "empty", Folder: true, DeletedAt: &old},
		{Path: "later.bin", Size: 9, Snap: "later", DeletedAt: &newer},
	}
	expired, protected, candidates := trashSnapshotPlan(all, cutoff)
	if len(expired) != 3 {
		t.Fatalf("expired entries=%+v", expired)
	}
	if _, ok := protected["shared"]; !ok {
		t.Fatal("snapshot shared with a live file was not protected")
	}
	if _, ok := protected["later"]; !ok {
		t.Fatal("snapshot referenced by newer Trash was not protected")
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate snapshots=%v", candidates)
	}
	if _, ok := candidates["shared"]; !ok {
		t.Fatal("expired zero-byte file snapshot was omitted")
	}
	if _, ok := candidates["orphan"]; !ok {
		t.Fatal("expired file snapshot was omitted")
	}
}

func TestCleanupTrashPurgesZeroByteFilesAndEmptyFolders(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("maintenance-test"), []byte("maintenance-test")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	if err := lib.Mkdir(ctx, "empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.PutReader(ctx, "zero.bin", bytes.NewReader(nil), 0); err != nil {
		t.Fatal(err)
	}
	if err := lib.Delete("empty"); err != nil {
		t.Fatal(err)
	}
	if err := lib.Delete("zero.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CleanupTrash(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("cleanup err=%v", err)
	}
	trash, err := lib.Trash(ctx)
	if err != nil || len(trash) != 0 {
		t.Fatalf("zero-byte/empty-folder entries remain in Trash: %+v err=%v", trash, err)
	}
	pass, _, err := v.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := lib.restic.Snapshots(ctx, restic.Repo{Location: lib.repo, Password: pass})
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("zero-byte file snapshot remains: %v err=%v", snapshots, err)
	}
	if _, err := lib.Put(ctx, "replacement.bin", []byte("old bytes")); err != nil {
		t.Fatal(err)
	}
	if err := lib.Delete("replacement.bin"); err != nil {
		t.Fatal(err)
	}
	newFile, err := lib.Put(ctx, "replacement.bin", []byte("new bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.List()) != 1 || len(mustTrash(t, lib, ctx)) != 1 {
		t.Fatal("replacement did not preserve one trashed version and one live version")
	}
	if _, err := lib.CleanupTrash(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Get(ctx, "replacement.bin")
	if err != nil || !bytes.Equal(got, []byte("new bytes")) {
		t.Fatalf("cleanup changed replacement content: %q err=%v", got, err)
	}
	snapshots, err = lib.restic.Snapshots(ctx, restic.Repo{Location: lib.repo, Password: pass})
	if err != nil || len(snapshots) != 1 || snapshots[0] != newFile.Snap {
		t.Fatalf("cleanup did not preserve only the live snapshot: %v err=%v", snapshots, err)
	}
}

func TestCleanupTrashRetriesFailedPruneAndMeasuresRepositoryReclaim(t *testing.T) {
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("maintenance-test"), []byte("maintenance-test")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	if _, err := lib.Put(ctx, "expired.bin", bytes.Repeat([]byte("reclaim-me"), 32*1024)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Delete("expired.bin"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "failed-once")
	shim := filepath.Join(dir, "restic-shim")
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = forget ] && [ ! -e \"" + marker + "\" ]; then\n    touch \"" + marker + "\"\n    echo injected-prune-failure >&2\n    exit 1\n  fi\ndone\nexec \"" + resticPath + "\" \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	lib.restic.Binary = shim
	cutoff := time.Now().UTC().Add(time.Hour)
	if _, err := lib.CleanupTrash(ctx, cutoff); err == nil || !strings.Contains(err.Error(), "injected-prune-failure") {
		t.Fatalf("expected injected prune failure, got %v", err)
	}
	if _, err := os.Stat(lib.trashIntentPath()); err != nil {
		t.Fatalf("cleanup intent missing after failed prune: %v", err)
	}
	if got := len(mustTrash(t, lib, ctx)); got != 0 {
		t.Fatalf("expired trash entries remain after catalog purge: %d", got)
	}
	reclaimed, err := lib.CleanupTrash(ctx, cutoff)
	if err != nil {
		t.Fatalf("retry cleanup failed: %v", err)
	}
	if reclaimed <= 0 {
		t.Fatalf("physical repository reclaim=%d bytes, want positive", reclaimed)
	}
	if _, err := os.Stat(lib.trashIntentPath()); !os.IsNotExist(err) {
		t.Fatalf("cleanup intent remains after successful retry: %v", err)
	}
}

func mustTrash(t *testing.T, lib *Library, ctx context.Context) []catalog.File {
	t.Helper()
	items, err := lib.Trash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return items
}
