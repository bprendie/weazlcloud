package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func inventoryAll(ctx context.Context, dataDir string, store *users.Store, action string) (Report, error) {
	report := Report{Action: action}
	report.Paused = exists(filepath.Join(dataDir, "migration.pause"))
	report.UnassignedLegacy = exists(filepath.Join(dataDir, "catalog.enc"))
	seenSnapshots := make(map[string]struct{})
	for _, user := range store.Users() {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Users++
		if user.Deleting {
			continue
		}
		if user.Disabled || user.DisablePending {
			report.DisabledUsers++
		}
		pending, pendingErr := pendingUploads(dataDir, store, user)
		report.PendingUploads += pending
		if pendingErr != nil {
			report.BlockedUsers++
			continue
		}
		v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
		if !v.Exists() || v.UnlockNode() != nil {
			report.BlockedUsers++
			continue
		}
		c := catalog.New(store.CatalogPath(user), v)
		loadErr := c.LoadReadOnly()
		v.Lock()
		if loadErr != nil {
			report.BlockedUsers++
			continue
		}
		repo := store.LibraryPath(user)
		report.SourceAllocatedBytes += allocated(repo)
		if entries, e := os.ReadDir(filepath.Join(repo, ".staging")); e == nil {
			report.StagedRecords += len(entries)
		}
		for _, file := range c.All() {
			if file.Folder {
				report.Folders++
				continue
			}
			if !file.Present && file.DeletedAt == nil {
				continue
			}
			report.LogicalBytes += file.Size
			if file.Present {
				report.LiveFiles++
			} else {
				report.TrashFiles++
				if file.DeletedAt.Before(time.Now().UTC().Add(-trashLifetime)) {
					report.ExpiredTrashFiles++
					continue
				}
			}
			if file.Reference != nil && file.Reference.Backend == catalog.SharedBackend {
				report.SharedFiles++
				continue
			}
			if !user.Disabled && !user.DisablePending && validSource(file) {
				report.EligibleFiles++
				if file.Reference != nil && file.Reference.Backend == catalog.ResticBackend {
					seenSnapshots[file.Reference.Snapshot] = struct{}{}
				}
				workspace, e := quota.SharedWriteWorkspace(file.Size)
				reservation, reservationErr := quota.SharedWriteReservation(file.Size, 0)
				if e != nil || reservationErr != nil || file.Size > int64(^uint64(0)>>1)-workspace || report.WorstCaseDestination > int64(^uint64(0)>>1)-file.Size-workspace {
					report.BlockedFiles++
				} else {
					report.WorstCaseDestination += file.Size + workspace
					if reservation > report.PeakFileReservation {
						report.PeakFileReservation = reservation
					}
				}
			} else {
				report.BlockedFiles++
			}
		}
	}
	report.LegacySnapshots = len(seenSnapshots)
	return report, nil
}

func validSource(file catalog.File) bool {
	if file.Size < 0 || len(file.Hash) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(file.Hash); err != nil {
		return false
	}
	return catalog.ValidateFileReference(file) == nil && file.Reference != nil && file.Reference.Backend == catalog.ResticBackend
}

func allocated(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return nil
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			total += stat.Blocks * 512
		} else {
			total += info.Size()
		}
		return nil
	})
	return total
}

func pendingUploads(dataDir string, store *users.Store, user users.User) (int, error) {
	dataPath, err := store.DataPath(user)
	if err != nil {
		return 0, err
	}
	root := filepath.Join(dataDir, "uploads", filepath.Base(dataPath))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}
	return count, nil
}
