package migration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestMigrationRunResumesVerifiesAndKeepsResticSources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := newMigrationUsers(t, root)
	alice := createMigrationUser(t, store, "alice")
	populateMigrationLibrary(t, ctx, alice)
	trash, err := alice.lib.Trash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var trashTime time.Time
	for _, file := range trash {
		if file.Path == "versions/same.txt" {
			trashTime = *file.DeletedAt
		}
	}
	if trashTime.IsZero() {
		t.Fatal("same-path Trash fixture is missing")
	}
	ageTrash(t, alice.vault, store.CatalogPath(alice.user), "versions/expired.txt", time.Now().Add(-31*24*time.Hour))
	beforeSnapshots := resticSnapshots(t, ctx, alice)

	stageMigrationBlockers(t, root, store, alice.user)
	_ = os.WriteFile(filepath.Join(root, "catalog.enc"), []byte("legacy-root-catalog"), 0o600)
	beforeTree := snapshotTree(t, root)
	dryRun, err := runMigration(t, ctx, root, "dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.UnassignedLegacy != true || dryRun.BlockedUsers != 1 || dryRun.StagedRecords != 1 || dryRun.PendingUploads != 1 {
		t.Fatalf("dry-run blockers not reported: %+v", dryRun)
	}
	if dryRun.LegacySnapshots != 4 || dryRun.ExpiredTrashFiles != 1 || dryRun.EligibleFiles != 5 || dryRun.RemainingFiles != 5 {
		t.Fatalf("dry-run inventory mismatch: %+v", dryRun)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(beforeTree, after) {
		t.Fatal("dry-run modified files or metadata in the data directory")
	}
	if strings.Contains(string(mustJSON(t, dryRun)), "alice") || strings.Contains(string(mustJSON(t, dryRun)), "versions/") {
		t.Fatal("migration report exposed a username or file path")
	}
	unassigned, err := runMigration(t, ctx, root, "start")
	if err == nil || !unassigned.UnassignedLegacy || unassigned.MigratedFiles != 0 {
		t.Fatalf("migration proceeded with an unassigned legacy catalog: report=%+v err=%v", unassigned, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(beforeTree, after) {
		t.Fatal("blocked start created migration state before resolving the legacy root catalog")
	}
	if err = os.Remove(filepath.Join(root, "catalog.enc")); err != nil {
		t.Fatal(err)
	}

	if _, err = runMigration(t, ctx, root, "pause"); err != nil {
		t.Fatal(err)
	}
	paused, err := runMigration(t, ctx, root, "start")
	if err != nil || !paused.Paused || paused.MigratedFiles != 0 {
		t.Fatalf("start ignored pause marker: report=%+v err=%v", paused, err)
	}
	blocked, err := runMigration(t, ctx, root, "resume")
	if err != nil || blocked.MigratedFiles != 0 || blocked.RemainingFiles != 5 {
		t.Fatalf("pending upload account was not held back: report=%+v err=%v", blocked, err)
	}
	incomplete, err := runMigration(t, ctx, root, "verify")
	if err == nil || incomplete.RemainingFiles != 5 {
		t.Fatalf("verify incorrectly passed with legacy files remaining: report=%+v err=%v", incomplete, err)
	}
	clearMigrationBlockers(t, root, store, alice.user)
	migrated, err := runMigration(t, ctx, root, "resume")
	if err != nil || migrated.MigratedFiles != 5 || migrated.RemainingFiles != 0 {
		t.Fatalf("resume did not migrate remaining records: report=%+v err=%v", migrated, err)
	}
	repeated, err := runMigration(t, ctx, root, "resume")
	if err != nil || repeated.MigratedFiles != 0 || repeated.RemainingFiles != 0 {
		t.Fatalf("repeated resume was not idempotent: report=%+v err=%v", repeated, err)
	}
	status, err := runMigration(t, ctx, root, "status")
	var expectedBytes int64 = int64(len("old version"))
	for _, content := range migrationContents() {
		expectedBytes += int64(len(content))
	}
	if err != nil || tallyState(status.Journal, "complete") != 5 || status.CompletedFiles != 5 || status.CompletedBytes != expectedBytes || status.FailedFiles != 0 {
		t.Fatalf("status lost durable completion state: report=%+v err=%v", status, err)
	}
	verified, err := runMigration(t, ctx, root, "verify")
	if err != nil || verified.MigratedFiles != 5 || verified.RemainingFiles != 0 {
		t.Fatalf("full verification failed: report=%+v err=%v", verified, err)
	}
	if _, err = runMigration(t, ctx, root, "retire"); err == nil || !strings.Contains(err.Error(), "D6") {
		t.Fatalf("legacy retirement was not held behind D6: %v", err)
	}

	afterSnapshots := resticSnapshots(t, ctx, alice)
	if !reflect.DeepEqual(beforeSnapshots, afterSnapshots) {
		t.Fatal("migration changed the legacy Restic snapshot set")
	}
	shared, err := sharedstore.Open(root, sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	reopenedVault := vault.New(store.VaultPath(alice.user), store.NodeKeyPath(alice.user))
	if err = reopenedVault.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	reopened := library.New(store.LibraryPath(alice.user), store.CatalogPath(alice.user), reopenedVault)
	reopened.ConfigureShared(alice.user.ID, shared, false)
	for name, want := range migrationContents() {
		got, getErr := reopened.Get(ctx, name)
		if getErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("post-migration read %q: bytes=%d err=%v", name, len(got), getErr)
		}
	}
	postFiles, err := reopened.MigrationFiles(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var preservedTrash, expiredRestic bool
	for _, file := range postFiles {
		if file.Path == "versions/same.txt" && !file.Present && file.DeletedAt != nil {
			preservedTrash = file.DeletedAt.Equal(trashTime) && file.Reference != nil && file.Reference.Backend == catalog.SharedBackend
		}
		if file.Path == "versions/expired.txt" && !file.Present {
			expiredRestic = file.DeletedAt.Before(time.Now().Add(-30*24*time.Hour)) && file.Reference != nil && file.Reference.Backend == catalog.ResticBackend
		}
	}
	if !preservedTrash || !expiredRestic {
		t.Fatalf("migration changed Trash state: preserved=%v expired_restic=%v", preservedTrash, expiredRestic)
	}
}

func TestDryRunReportsUnreadableAccountWithoutWriting(t *testing.T) {
	root := t.TempDir()
	store := newMigrationUsers(t, root)
	user, err := store.Create("locked", "test-password-123", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(store.LibraryPath(user), ".staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(store.LibraryPath(user), ".staging", "pending"), []byte("stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "uploads", user.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "uploads", user.ID, "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "catalog.enc"), []byte("unassigned"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)
	report, err := runMigration(t, context.Background(), root, "dry-run")
	if err != nil || report.BlockedUsers != 1 || report.StagedRecords != 1 || report.PendingUploads != 1 || !report.UnassignedLegacy {
		t.Fatalf("unreadable account blockers missing: report=%+v err=%v", report, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only dry-run wrote an upgrade to account or catalog state")
	}
}

func TestPendingUploadsIgnoresCompletedSessionManifests(t *testing.T) {
	root := t.TempDir()
	store := newMigrationUsers(t, root)
	user, err := store.Create("uploader", "test-password-123", false)
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(root, "uploads", user.ID)
	if err = os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	complete := `{"status":"complete"}`
	if err = os.WriteFile(filepath.Join(sessions, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"), []byte(complete), 0o600); err != nil {
		t.Fatal(err)
	}
	count, err := pendingUploads(root, store, user)
	if err != nil || count != 0 {
		t.Fatalf("completed upload blocked migration: count=%d err=%v", count, err)
	}
	if err = os.WriteFile(filepath.Join(sessions, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json"), []byte(`{"status":"ready"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	count, err = pendingUploads(root, store, user)
	if err != nil || count != 1 {
		t.Fatalf("live upload was not counted: count=%d err=%v", count, err)
	}
}
