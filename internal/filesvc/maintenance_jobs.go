package filesvc

import (
	"context"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (r *Registry) RecoverStaging(ctx context.Context) (int64, error) {
	var reclaimed int64
	for _, user := range r.users.Users() {
		if user.Disabled || user.Deleting {
			continue
		}
		resource := r.For(user)
		if !resource.Vault.Exists() {
			continue
		}
		locked := !resource.Vault.Unlocked()
		if locked {
			if err := resource.Vault.UnlockNode(); err != nil {
				return reclaimed, err
			}
		}
		bytes, err := func() (int64, error) {
			if locked {
				defer resource.Vault.Lock()
			}
			return resource.Lib.RecoverStaging(ctx)
		}()
		reclaimed += bytes
		if err != nil {
			return reclaimed, err
		}
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
	}
	return reclaimed, nil
}

func (r *Registry) CleanupPreviewCaches(ctx context.Context) (int64, error) {
	var reclaimed int64
	for _, user := range r.users.Users() {
		if user.Deleting {
			continue
		}
		root, err := r.users.DataPath(user)
		if err != nil {
			return reclaimed, err
		}
		bytes, err := library.CleanupThumbnailCache(filepath.Join(root, ".weazl-previews"))
		reclaimed += bytes
		if err != nil {
			return reclaimed, err
		}
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
	}
	return reclaimed, nil
}

func (r *Registry) CleanupExpiredArchives(ctx context.Context) (int64, error) {
	var reclaimed int64
	for _, user := range r.users.Users() {
		if user.Disabled || user.Deleting {
			continue
		}
		resource := r.For(user)
		if !resource.Vault.Exists() {
			continue
		}
		bytes, err := resource.Archives.CleanupExpired(time.Now().UTC())
		reclaimed += bytes
		if err != nil {
			return reclaimed, err
		}
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
	}
	return reclaimed, nil
}
