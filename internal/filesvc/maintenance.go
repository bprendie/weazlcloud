package filesvc

import (
	"context"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (r *Registry) CleanupExpiredTrash(ctx context.Context) error {
	_, err := r.CleanupExpiredTrashBytes(ctx)
	return err
}

func (r *Registry) CleanupExpiredTrashBytes(ctx context.Context) (int64, error) {
	cutoff := time.Now().UTC().Add(-library.TrashLifetime)
	var reclaimed int64
	for _, user := range r.users.Users() {
		if user.Disabled || user.Deleting {
			continue
		}
		resource := r.For(user)
		if !resource.Vault.Exists() {
			continue
		}
		lockedForMaintenance := !resource.Vault.Unlocked()
		if lockedForMaintenance {
			if err := resource.Vault.UnlockNode(); err != nil {
				return reclaimed, err
			}
		}
		cleanupErr := func() error {
			if lockedForMaintenance {
				defer resource.Vault.Lock()
			}
			bytes, err := resource.Lib.CleanupTrash(ctx, cutoff)
			reclaimed += bytes
			return err
		}()
		if cleanupErr != nil {
			return reclaimed, cleanupErr
		}
		if err := ctx.Err(); err != nil {
			return reclaimed, err
		}
	}
	return reclaimed, nil
}
