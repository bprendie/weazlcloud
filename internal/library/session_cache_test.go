package library

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestCatalogSnapshotClearsAtVaultLock(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("cache-pass"), []byte("cache-pass")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog.enc")
	seed := catalog.New(path, v)
	if err := seed.Put(catalog.File{Path: "visible.txt", Size: 4, Hash: "abcd", Present: true}); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(root, "repo"), path, v)
	l.backend = &isolatedLegacy{root: filepath.Join(root, "repo")}
	ctx := context.Background()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	loadedVersion := l.catalog.Version()
	if err := l.Ensure(ctx); err != nil || len(l.List()) != 1 {
		t.Fatalf("warm read unexpectedly reloaded the catalog: %v", err)
	}
	if got := l.catalog.Version(); got != loadedVersion {
		t.Fatalf("warm catalog version changed from %d to %d", loadedVersion, got)
	}
	external := catalog.New(path, v)
	if err := external.Load(); err != nil {
		t.Fatal(err)
	}
	if err := external.Put(catalog.File{Path: "outside.txt", Size: 3, Hash: "efgh", Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(ctx); err != nil || len(l.List()) != 2 {
		t.Fatalf("external catalog replacement was not reloaded: %v", err)
	}
	v.Lock()
	l.ForgetVaultSession()
	if got := len(l.catalog.List()); got != 0 {
		t.Fatalf("plaintext catalog remained after lock: %d entries", got)
	}
	if err := v.Unlock([]byte("cache-pass")); err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(ctx); err != nil || len(l.List()) != 2 {
		t.Fatalf("catalog did not reload after unlock: %v", err)
	}
}
