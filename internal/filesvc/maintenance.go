package filesvc

import (
	"context"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (r *Registry) CleanupExpiredTrash(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-library.TrashLifetime)
	for _, user := range r.users.Users() {
		if user.Disabled {
			continue
		}
		resource := r.For(user)
		if !resource.Vault.Exists() {
			continue
		}
		lockedForMaintenance := !resource.Vault.Unlocked()
		if lockedForMaintenance {
			if err := resource.Vault.UnlockNode(); err != nil {
				return err
			}
		}
		cleanupErr := func() error {
			if lockedForMaintenance {
				defer resource.Vault.Lock()
			}
			_, err := resource.Lib.CleanupTrash(ctx, cutoff)
			return err
		}()
		if cleanupErr != nil {
			return cleanupErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
