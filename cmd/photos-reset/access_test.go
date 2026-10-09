package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func TestRewritePreservesServiceAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.enc")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	// The root Docker regression exercises the production failure: an atomic
	// rewrite must leave a private catalog readable by UID 7272, not just root.
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 7272, 7272); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.Stat(path)
	if err := rewritePreservingAccess(path, func() error {
		return cryptox.AtomicWrite(path, []byte("after"), 0644)
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if a.Uid != b.Uid || a.Gid != b.Gid || after.Mode().Perm() != 0600 {
		t.Fatal("atomic rewrite changed private catalog ownership or permissions")
	}
	err = requireCatalogOwner(path)
	if os.Geteuid() == 0 && err == nil {
		t.Fatal("root was allowed to rewrite service-owned Restic packs")
	}
	if os.Geteuid() != 0 && err != nil {
		t.Fatal(err)
	}
}

func TestRewriteFailurePreservesErrorAndAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.enc")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("write failed")
	if err := rewritePreservingAccess(path, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("lost rewrite failure: %v", err)
	}
}
