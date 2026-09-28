package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

// Migration needs two catalog references to one real legacy snapshot. Construct
// that snapshot explicitly: goroutines need not reach the upload queue within
// its 35ms batching window on a loaded CI runner.
func populateLegacyBatchSnapshot(t *testing.T, ctx context.Context, account migrationAccount) {
	t.Helper()
	root := t.TempDir()
	items := map[string][]byte{
		"batch/one.txt": []byte("first batch item"),
		"batch/two.txt": []byte("second batch item"),
	}
	for name, content := range items {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	password, _, err := account.vault.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(password)
	snapshot, err := restic.New().PutBatch(ctx, restic.Repo{Location: account.store.LibraryPath(account.user), Password: password}, root)
	if err != nil {
		t.Fatal(err)
	}
	c := catalog.New(account.store.CatalogPath(account.user), account.vault)
	if err = c.Load(); err != nil {
		t.Fatal(err)
	}
	for name, content := range items {
		object := filepath.Join(root, filepath.FromSlash(name))
		hash := sha256.Sum256(content)
		ref := &catalog.Reference{Backend: catalog.ResticBackend, Version: 1, Snapshot: snapshot, Object: object}
		err = c.Put(catalog.File{Path: name, Size: int64(len(content)), Mtime: time.Now().UTC(), Hash: hex.EncodeToString(hash[:]), Snap: snapshot, Object: object, Reference: ref, Present: true})
		if err != nil {
			t.Fatal(err)
		}
	}
}
