package migration

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func migrateAll(ctx context.Context, dataDir string, store *users.Store, shared *sharedstore.Store) (Report, error) {
	report, err := inventoryAll(ctx, dataDir, store, "start")
	if err != nil {
		return report, err
	}
	if report.UnassignedLegacy {
		return report, errors.New("legacy root catalog must be assigned to an account before migration")
	}
	report.BlockedUsers = 0
	report.BlockedFiles = 0
	quotaManager := quota.New(dataDir)
	usersCount := store.Count()
	for _, user := range store.Users() {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		if user.Deleting || user.Disabled || user.DisablePending {
			continue
		}
		pending, pendingErr := pendingUploads(dataDir, store, user)
		if pendingErr != nil || pending != 0 {
			report.BlockedUsers++
			continue
		}
		v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
		if !v.Exists() {
			report.BlockedUsers++
			continue
		}
		if err = v.UnlockNode(); err != nil {
			report.BlockedUsers++
			continue
		}
		lib := library.New(store.LibraryPath(user), store.CatalogPath(user), v)
		lib.ConfigureShared(user.ID, shared, false)
		if err = lib.ReconcileShared(ctx); err != nil {
			v.Lock()
			report.BlockedUsers++
			continue
		}
		files, listErr := lib.MigrationFiles(ctx, false)
		if listErr != nil {
			v.Lock()
			report.BlockedUsers++
			continue
		}
		for _, file := range files {
			if file.Reference != nil && file.Reference.Backend == catalog.SharedBackend && file.Reference.OwnerRevision > 1 {
				if err = shared.FinishMigration(ctx, user.ID, file.EntryID, file.Reference.OwnerRevision-1, file.Size); err != nil {
					v.Lock()
					return report, err
				}
			}
			if file.Folder || (!file.Present && (file.DeletedAt == nil || file.DeletedAt.Before(time.Now().UTC().Add(-trashLifetime)))) || (file.Reference != nil && file.Reference.Backend == catalog.SharedBackend) {
				continue
			}
			if exists(filepath.Join(dataDir, "migration.pause")) {
				report.Paused = true
				break
			}
			if user.Disabled || user.DisablePending || !validSource(file) {
				report.BlockedFiles++
				if err = shared.RecordMigration(ctx, user.ID, file.EntryID, file.Revision, "blocked", 0, "source-metadata"); err != nil {
					v.Lock()
					return report, err
				}
				continue
			}
			if err = shared.RecordMigration(ctx, user.ID, file.EntryID, file.Revision, "discovered", 0, ""); err != nil {
				v.Lock()
				return report, err
			}
			progress := func(state string, bytes int64) error {
				return shared.RecordMigration(ctx, user.ID, file.EntryID, file.Revision, state, bytes, "")
			}
			reserve := func(size int64) (func(), error) {
				bytes, reserveErr := quota.SharedWriteReservation(size, 0)
				if reserveErr != nil {
					return nil, reserveErr
				}
				return quotaManager.Reserve(user.ID, usersCount, 0, 0, bytes)
			}
			if err = lib.MigrateToShared(ctx, file, reserve, progress); err != nil {
				category := errorCategory(err)
				if e := shared.RecordMigration(ctx, user.ID, file.EntryID, file.Revision, "blocked", 0, category); e != nil {
					v.Lock()
					return report, e
				}
				report.BlockedFiles++
				continue
			}
			report.MigratedFiles++
		}
		v.Lock()
		if report.Paused {
			break
		}
	}
	report.Action = "start"
	return report, nil
}

func verifyAll(ctx context.Context, store *users.Store, shared *sharedstore.Store) (Report, error) {
	report := Report{Action: "verify"}
	for _, user := range store.Users() {
		if user.Deleting {
			continue
		}
		v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
		if !v.Exists() || v.UnlockNode() != nil {
			report.BlockedUsers++
			continue
		}
		lib := library.New(store.LibraryPath(user), store.CatalogPath(user), v)
		lib.ConfigureShared(user.ID, shared, false)
		files, err := lib.MigrationFiles(ctx, true)
		if err != nil {
			report.BlockedUsers++
			v.Lock()
			continue
		}
		for _, file := range files {
			if file.Folder || file.Reference == nil || file.Reference.Backend != catalog.SharedBackend {
				continue
			}
			if err = lib.VerifyMigratedFile(ctx, file); err != nil {
				report.BlockedFiles++
			} else {
				report.MigratedFiles++
			}
		}
		v.Lock()
	}
	return report, nil
}
