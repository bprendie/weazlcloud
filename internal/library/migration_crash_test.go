package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestMigrationReconcilesProcessStopsAtEveryDurableStage(t *testing.T) {
	for _, stage := range []string{"discovered", "source-pinned", "copied", "destination-verified", "catalog-switched"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			lib, _, store, target, content := migrationTestLibrary(t)
			root := filepath.Dir(filepath.Dir(lib.repo))
			if err := store.RecordMigration(ctx, "migration-owner", target.EntryID, target.Revision, "discovered", 0, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			lib.vault.Lock()
			cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationCrashChild$")
			cmd.Env = append(os.Environ(), "WEAZLCLOUD_MIGRATION_TEST_ROOT="+root, "WEAZLCLOUD_MIGRATION_CRASH_AT="+stage)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 86 {
				t.Fatalf("child did not stop at %s: err=%v output=%s", stage, err, output)
			}
			reconcileMigrationRestart(t, ctx, root, lib.backend, target, content)
		})
	}
}

// TestMigrationCrashChild is launched by the parent test and exits without
// running defers to model an abrupt process stop at a selected durable stage.
func TestMigrationCrashChild(t *testing.T) {
	root := os.Getenv("WEAZLCLOUD_MIGRATION_TEST_ROOT")
	stage := os.Getenv("WEAZLCLOUD_MIGRATION_CRASH_AT")
	if root == "" || stage == "" {
		return
	}
	ctx := context.Background()
	userRoot := filepath.Join(root, "user")
	v := vault.New(filepath.Join(userRoot, "vault.json"), filepath.Join(userRoot, "node.key"))
	if err := v.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(userRoot, "library")
	legacy := &immutableLegacy{isolatedLegacy: isolatedLegacy{root: repo}}
	lib := New(repo, filepath.Join(userRoot, "catalog.enc"), v)
	lib.backend = legacy
	store, err := sharedstore.Open(filepath.Join(root, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lib.ConfigureShared("migration-owner", store, false)
	target, err := lib.Metadata(ctx, "file.bin")
	if err != nil {
		t.Fatal(err)
	}
	if stage == "discovered" {
		os.Exit(86)
	}
	reserve := func(int64) (func(), error) { return func() {}, nil }
	progress := func(state string, count int64) error {
		if err := store.RecordMigration(ctx, "migration-owner", target.EntryID, target.Revision, state, count, ""); err != nil {
			return err
		}
		if state == stage {
			os.Exit(86)
		}
		return nil
	}
	if err = lib.MigrateToShared(ctx, target, reserve, progress); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("migration passed requested crash point %s", stage)
}

func reconcileMigrationRestart(t *testing.T, ctx context.Context, root string, backend Backend, target catalog.File, content []byte) {
	t.Helper()
	userRoot := filepath.Join(root, "user")
	v := vault.New(filepath.Join(userRoot, "vault.json"), filepath.Join(userRoot, "node.key"))
	if err := v.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	store, err := sharedstore.Open(filepath.Join(root, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo := filepath.Join(userRoot, "library")
	lib := New(repo, filepath.Join(userRoot, "catalog.enc"), v)
	lib.backend = backend
	lib.ConfigureShared("migration-owner", store, false)
	if err = lib.ReconcileShared(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := lib.MigrationFiles(ctx, true)
	if err != nil || len(files) != 1 {
		t.Fatalf("catalog failed to reopen: files=%+v err=%v", files, err)
	}
	if files[0].Reference == nil || files[0].Reference.Backend != catalog.SharedBackend {
		got, readErr := lib.Get(ctx, files[0].Path)
		if readErr != nil || string(got) != string(content) {
			t.Fatalf("legacy reference failed after restart: %v", readErr)
		}
		reserve := func(int64) (func(), error) { return func() {}, nil }
		progress := func(state string, count int64) error {
			return store.RecordMigration(ctx, "migration-owner", target.EntryID, target.Revision, state, count, "")
		}
		if err = lib.MigrateToShared(ctx, files[0], reserve, progress); err != nil {
			t.Fatalf("resume after %s did not converge: %v", files[0].Path, err)
		}
		files, err = lib.MigrationFiles(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = store.FinishMigration(ctx, "migration-owner", target.EntryID, target.Revision, target.Size); err != nil {
		t.Fatal(err)
	}
	if err = lib.VerifyMigratedFile(ctx, files[0]); err != nil {
		t.Fatalf("recovered destination did not verify: %v", err)
	}
	status, err := store.MigrationStatus(ctx)
	if err != nil || len(status) != 1 || status[0].State != "complete" {
		t.Fatalf("journal did not converge after restart: status=%+v err=%v", status, err)
	}
}
