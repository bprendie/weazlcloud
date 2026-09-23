package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type migrationAccount struct {
	user  users.User
	vault *vault.Vault
	lib   *library.Library
	store *users.Store
}

func createMigrationUser(t *testing.T, store *users.Store, username string) migrationAccount {
	t.Helper()
	user, err := store.Create(username, "test-password-123", false)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
	if err = v.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(store.LibraryPath(user), store.CatalogPath(user), v)
	if err = lib.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return migrationAccount{user: user, vault: v, lib: lib, store: store}
}

func populateMigrationLibrary(t *testing.T, ctx context.Context, account migrationAccount) {
	t.Helper()
	if err := account.lib.Mkdir(ctx, "batch"); err != nil {
		t.Fatal(err)
	}
	if err := account.lib.Mkdir(ctx, "versions"); err != nil {
		t.Fatal(err)
	}
	if _, err := account.lib.Put(ctx, "versions/same.txt", []byte("old version")); err != nil {
		t.Fatal(err)
	}
	if err := account.lib.Delete("versions/same.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := account.lib.Put(ctx, "versions/same.txt", []byte("new version")); err != nil {
		t.Fatal(err)
	}
	if _, err := account.lib.Put(ctx, "versions/expired.txt", []byte("expired trash")); err != nil {
		t.Fatal(err)
	}
	if err := account.lib.Delete("versions/expired.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := account.lib.Put(ctx, "empty.bin", nil); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct{ path, content string }{{"batch/one.txt", "first batch item"}, {"batch/two.txt", "second batch item"}} {
		wg.Add(1)
		go func(path, content string) {
			defer wg.Done()
			<-start
			_, putErr := account.lib.Put(ctx, path, []byte(content))
			results <- putErr
		}(item.path, item.content)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	files, err := account.lib.MigrationFiles(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	batchSnapshots := map[string]bool{}
	for _, file := range files {
		if file.Path == "batch/one.txt" || file.Path == "batch/two.txt" {
			batchSnapshots[file.Reference.Snapshot] = true
		}
	}
	if len(batchSnapshots) != 1 {
		t.Fatal("legacy batch fixture did not share one Restic snapshot")
	}
}

func migrationContents() map[string][]byte {
	return map[string][]byte{
		"versions/same.txt": []byte("new version"),
		"batch/one.txt":     []byte("first batch item"),
		"batch/two.txt":     []byte("second batch item"),
		"empty.bin":         {},
	}
}

func stageMigrationBlockers(t *testing.T, root string, store *users.Store, user users.User) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(store.LibraryPath(user), ".staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.LibraryPath(user), ".staging", "pending"), []byte("stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "uploads", user.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "uploads", user.ID, "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func clearMigrationBlockers(t *testing.T, root string, store *users.Store, user users.User) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(store.LibraryPath(user), ".staging")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "uploads", user.ID)); err != nil {
		t.Fatal(err)
	}
}

func ageTrash(t *testing.T, v *vault.Vault, path, entry string, at time.Time) {
	t.Helper()
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := v.Unwrap(sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var tree struct {
		Files []catalog.File `json:"files"`
	}
	if err = json.Unmarshal(plain, &tree); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range tree.Files {
		if tree.Files[i].Path == entry && !tree.Files[i].Present {
			tree.Files[i].DeletedAt = &at
			found = true
		}
	}
	if !found {
		t.Fatalf("Trash entry %q not found", entry)
	}
	updated, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	updatedSealed, err := v.Wrap(updated)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, updatedSealed, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newMigrationUsers(t *testing.T, root string) *users.Store {
	t.Helper()
	store, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resticSnapshots(t *testing.T, ctx context.Context, account migrationAccount) []string {
	t.Helper()
	password, _, err := account.vault.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(password)
	ids, err := restic.New().Snapshots(ctx, restic.Repo{Location: account.store.LibraryPath(account.user), Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func runMigration(t *testing.T, ctx context.Context, root, action string) (Report, error) {
	t.Helper()
	var output bytes.Buffer
	err := Run(ctx, root, action, &output)
	var report Report
	if output.Len() == 0 {
		return report, err
	}
	if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode %s report %q: %v", action, output.String(), decodeErr)
	}
	return report, err
}

func tallyState(tallies []sharedstore.MigrationTally, state string) int64 {
	for _, tally := range tallies {
		if tally.State == state {
			return tally.Items
		}
	}
	return 0
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stamp := info.Mode().String() + ":" + info.ModTime().UTC().Format(time.RFC3339Nano)
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(contents)
			stamp += ":" + hex.EncodeToString(hash[:])
		}
		tree[rel] = stamp
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
