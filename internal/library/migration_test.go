package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestMigrationLeavesLegacyReadableOnQuotaAndSourceFailures(t *testing.T) {
	ctx := context.Background()
	lib, legacy, store, target, content := migrationTestLibrary(t)
	reserveFailure := func(int64) (func(), error) { return nil, quota.ErrExceeded }
	if err := lib.MigrateToShared(ctx, target, reserveFailure, nil); !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("quota failure: %v", err)
	}
	corrupt := append([]byte(nil), content...)
	corrupt[0] ^= 0xff
	legacy.files[target.Reference.Object] = corrupt
	reserve := func(int64) (func(), error) { return func() {}, nil }
	if err := lib.MigrateToShared(ctx, target, reserve, nil); err == nil {
		t.Fatal("accepted a source whose bytes do not match its catalog hash")
	}
	legacy.files[target.Reference.Object] = content
	got, err := lib.Get(ctx, target.Path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("legacy source became unreadable after migration failures: %v", err)
	}
	current, err := lib.Metadata(ctx, target.Path)
	if err != nil || current.Reference == nil || current.Reference.Backend != catalog.ResticBackend || current.Revision != target.Revision {
		t.Fatalf("failed migration changed the catalog: file=%+v err=%v", current, err)
	}
	_ = store.Close()
}

func TestMigrationRejectsChangedCatalogVersions(t *testing.T) {
	for _, action := range []string{"rename", "restore", "overwrite", "delete"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			lib, legacy, store, target, content := migrationTestLibrary(t)
			defer store.Close()
			switch action {
			case "rename":
				if err := lib.Rename(ctx, target.Path, "renamed.bin"); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := lib.Delete(target.Path); err != nil {
					t.Fatal(err)
				}
				if err := lib.Restore(ctx, target.Path); err != nil {
					t.Fatal(err)
				}
			case "overwrite":
				if _, err := lib.Put(ctx, target.Path, []byte("replacement bytes")); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := lib.Delete(target.Path); err != nil {
					t.Fatal(err)
				}
			}
			reserve := func(int64) (func(), error) { return func() {}, nil }
			if err := lib.MigrateToShared(ctx, target, reserve, nil); !errors.Is(err, catalog.ErrRevisionMismatch) {
				t.Fatalf("stale inventory item was not rejected: %v", err)
			}
			var source bytes.Buffer
			if err := legacy.Read(ctx, *target.Reference, &source); err != nil || !bytes.Equal(source.Bytes(), content) {
				t.Fatalf("original source was not preserved: %v", err)
			}
			if action != "delete" {
				path := target.Path
				if action == "rename" {
					path = "renamed.bin"
				}
				if action == "overwrite" {
					content = []byte("replacement bytes")
				}
				got, err := lib.Get(ctx, path)
				if err != nil || !bytes.Equal(got, content) {
					t.Fatalf("concurrent catalog change was damaged: %v", err)
				}
			}
		})
	}
}

func migrationTestLibrary(t *testing.T) (*Library, *immutableLegacy, *sharedstore.Store, catalog.File, []byte) {
	t.Helper()
	root := t.TempDir()
	userRoot := filepath.Join(root, "user")
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(userRoot, "vault.json"), filepath.Join(userRoot, "node.key"))
	if err := v.Forge([]byte("migration-pass"), []byte("migration-pass")); err != nil {
		t.Fatal(err)
	}
	legacy := &immutableLegacy{isolatedLegacy: isolatedLegacy{root: filepath.Join(userRoot, "library")}}
	lib := New(legacy.root, filepath.Join(userRoot, "catalog.enc"), v)
	lib.backend = legacy
	store, err := sharedstore.Open(filepath.Join(root, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lib.ConfigureShared("migration-owner", store, false)
	content := []byte("legacy migration fixture")
	if _, err = lib.Put(context.Background(), "file.bin", content); err != nil {
		t.Fatal(err)
	}
	target, err := lib.Metadata(context.Background(), "file.bin")
	if err != nil {
		t.Fatal(err)
	}
	return lib, legacy, store, target, content
}

type immutableLegacy struct {
	isolatedLegacy
	next int
}

func (b *immutableLegacy) Put(_ context.Context, name string, body io.Reader) (catalog.Reference, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return catalog.Reference{}, err
	}
	b.next++
	id := fmt.Sprintf("legacy-%d", b.next)
	if b.files == nil {
		b.files = make(map[string][]byte)
	}
	b.files[id] = content
	if err = os.WriteFile(filepath.Join(b.root, id), content, 0o600); err != nil {
		return catalog.Reference{}, err
	}
	return catalog.Reference{Backend: catalog.ResticBackend, Version: 1, Snapshot: id, Object: id}, nil
}
