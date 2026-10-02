package backup

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

// FinalizeParts consumes the parent's verified reader directly. The parent must
// retain its quota reservation and owner/device validity lease for this call.
func (m *Manager) FinalizeParts(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, body io.Reader) (View, error) {
	return m.FinalizeReader(ctx, res, user, id, device, body)
}
func (m *Manager) FinalizeReader(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, body io.Reader) (View, error) {
	var out View
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, id, device)
		if err != nil {
			return err
		}
		if err := recoverOperation(tx, &op); err != nil {
			if body == nil || !orphaned(err) || op.Status != "publishing" || tx.Catalog().BackupPublished(op.Plan.Files) {
				return err
			}
			if err := resetStorage(tx, &op); err != nil {
				return err
			}
		}
		if op.Status == "stored" {
			out = op.View
			return nil
		}
		if op.Status != "accepted" {
			return ErrPaused
		}
		src, err := loadSource(tx, op.SourceKey)
		if err != nil {
			return err
		}
		if err := recoverSource(tx, &src, op.SourceKey); err != nil {
			return err
		}
		if src.Source.Status != "active" {
			return ErrPaused
		}
		if err := adoptParents(tx, src, &op); err != nil {
			return err
		}
		if err := tx.Catalog().CheckBackup(op.Plan); err != nil {
			return err
		}
		for i, f := range op.Plan.Files {
			if f.EntryID != op.PrimaryID || f.Folder || f.Reference != nil {
				continue
			}
			storageID := op.StorageID
			if storageID == "" {
				storageID = op.ID
			}
			if op.StorageID != "" {
				f, err = tx.RetryStoreReader(storageID, f, body)
			} else {
				f, err = tx.StoreReader(storageID, f, body)
			}
			if orphaned(err) {
				if err := resetStorage(tx, &op); err != nil {
					return err
				}
				f, err = tx.RetryStoreReader(op.StorageID, op.Plan.Files[i], body)
			}
			if err != nil {
				return err
			}
			op.Plan.Files[i] = f
		}
		// Persist the exact backend reference/operation ID before catalog publication.
		op.Status = "publishing"
		if err := tx.Write(op.ID, op); err != nil {
			return err
		}
		src.Pending = op.ID
		if err := tx.Write(op.SourceKey, src); err != nil {
			return err
		}
		if err := recoverOperation(tx, &op); err != nil {
			return err
		}
		out = op.View
		return nil
	})
	return out, err
}

func recoverSource(tx *library.BackupTransaction, src *sourceRecord, key string) error {
	if src.Pending == "" {
		return nil
	}
	op, err := loadOperation(tx, src.Pending, src.Source.DeviceID)
	if err != nil {
		return err
	}
	if err := recoverOperation(tx, &op); err != nil {
		return err
	}
	refreshed, err := loadSource(tx, key)
	if err != nil {
		return err
	}
	if op.Status == "stored" {
		refreshed.Pending = ""
		if err := tx.Write(key, refreshed); err != nil {
			return err
		}
	}
	*src = refreshed
	return err
}

func recoverOperation(tx *library.BackupTransaction, op *operation) error {
	if op.Status != "publishing" {
		return nil
	}
	src, err := loadSource(tx, op.SourceKey)
	if err != nil {
		return err
	}
	if !tx.Catalog().BackupPublished(op.Plan.Files) {
		if src.Source.Status != "active" {
			return ErrPaused
		}
		if err := tx.CheckBackupStorage(op.Plan); err != nil {
			return err
		}
		if err := tx.Publish(op.Plan); err != nil {
			return err
		}
	}
	if err := tx.FinishBackup(op.Plan); err != nil {
		return err
	}
	// Refresh every mapped descendant changed by a folder rename, and preserve
	// stable IDs for implicit relative folders created by this operation.
	result := make(map[string]catalog.File)
	for _, f := range op.Plan.Files {
		result[f.EntryID] = f
	}
	for key, old := range src.Items {
		if f, ok := result[old.EntryID]; ok {
			src.Items[key] = f
		}
	}
	for _, f := range op.Plan.Files {
		if f.EntryID == op.PrimaryID {
			copied := f
			op.File = &copied
			src.Items[op.Spec.ItemID] = f
		}
		if f.Folder && f.EntryID != op.PrimaryID {
			rel := strings.TrimPrefix(f.Path, src.Destination.Path+"/")
			src.Items["\x00folder:"+rel] = f
		}
	}
	// The mapping is published before the receipt. Either write may be retried;
	// publication proof prevents another backend write or catalog revision bump.
	src.Pending = op.ID
	if err := tx.Write(op.SourceKey, src); err != nil {
		return err
	}
	op.Status = "stored"
	if err := tx.Write(op.ID, op); err != nil {
		return err
	}
	src.Pending = ""
	return tx.Write(op.SourceKey, src)
}

func orphaned(err error) bool {
	return errors.Is(err, sharedstore.ErrState) || errors.Is(err, sharedstore.ErrDenied)
}

// Shared owner reconciliation may abort an unpublished prepare after restart.
// Keep the immutable receipt ID; replace only the private storage attempt ID.
// The original catalog CAS must still match, so this cannot resurrect an entry.
func resetStorage(tx *library.BackupTransaction, op *operation) error {
	if err := tx.Catalog().CheckBackup(op.Plan); err != nil {
		return err
	}
	id, err := catalog.NewBackupIdentity()
	if err != nil {
		return err
	}
	op.StorageID = id
	op.Status = "accepted"
	for i, f := range op.Plan.Files {
		if f.EntryID == op.PrimaryID && !f.Folder {
			f.Reference = nil
			f.Snap = ""
			f.Object = ""
			op.Plan.Files[i] = f
		}
	}
	if err := tx.Write(op.ID, op); err != nil {
		return err
	}
	src, err := loadSource(tx, op.SourceKey)
	if err != nil {
		return err
	}
	if src.Pending == op.ID {
		src.Pending = ""
		return tx.Write(op.SourceKey, src)
	}
	return nil
}
