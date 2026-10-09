package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Restic creates replacement packs and indexes using the caller's identity.
// Verification must exercise that same service identity, not root's access.
func requireCatalogOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || int(stat.Gid) != os.Getegid() {
		return errors.New("run retirement and verification with the catalog service UID and GID")
	}
	return nil
}

// Atomic replacement inherits the maintenance process's UID. Preserve the
// service account's ownership before the server is allowed to start again.
func rewritePreservingAccess(path string, rewrite func() error) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() {
		return errors.New("catalog must be a regular file with POSIX ownership")
	}
	if os.Geteuid() != 0 && os.Geteuid() != int(stat.Uid) {
		return errors.New("run reset as the catalog owner or root")
	}
	rewriteErr := rewrite()
	after, err := os.Lstat(path)
	if err != nil {
		return errors.Join(rewriteErr, err)
	}
	current, ok := after.Sys().(*syscall.Stat_t)
	if !ok || !after.Mode().IsRegular() {
		return errors.Join(rewriteErr, errors.New("rewritten catalog is not a regular POSIX file"))
	}
	if current.Uid != stat.Uid || current.Gid != stat.Gid {
		if err := os.Chown(path, int(stat.Uid), int(stat.Gid)); err != nil {
			return errors.Join(rewriteErr, fmt.Errorf("restore catalog ownership; keep server stopped: %w", err))
		}
	}
	if err := os.Chmod(path, before.Mode().Perm()); err != nil {
		return errors.Join(rewriteErr, fmt.Errorf("restore catalog permissions; keep server stopped: %w", err))
	}
	return rewriteErr
}
