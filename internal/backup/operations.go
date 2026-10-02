package backup

import (
	"context"
	"io"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (m *Manager) Status(ctx context.Context, res *filesvc.Resource, user users.User, id, device string) (View, error) {
	var out View
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, id, device)
		if err != nil {
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
func (m *Manager) Append(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, offset, length int64, hash string, body io.Reader) (upload.SessionView, error) {
	v, err := m.Status(ctx, res, user, id, device)
	if err != nil {
		return upload.SessionView{}, err
	}
	if v.Status != "accepted" || v.Spec.Transport != "sequential" || v.Spec.Kind != "file" || m.uploads == nil {
		return upload.SessionView{}, ErrInvalid
	}
	if v.UploadID == "" {
		v, err = m.ensureSequential(ctx, res, user, v)
		if err != nil {
			return upload.SessionView{}, err
		}
	}
	return m.uploads.Append(ctx, user, v.UploadID, offset, length, hash, body)
}
func (m *Manager) Finalize(ctx context.Context, res *filesvc.Resource, user users.User, id, device string) (View, error) {
	v, err := m.Status(ctx, res, user, id, device)
	if err != nil || v.Status == "stored" {
		return v, err
	}
	if v.Spec.Kind == "folder" {
		return m.FinalizeReader(ctx, res, user, id, device, nil)
	}
	if v.Spec.Transport != "sequential" || m.uploads == nil {
		return v, ErrInvalid
	}
	if v.UploadID == "" {
		v, err = m.ensureSequential(ctx, res, user, v)
		if err != nil {
			return v, err
		}
	}
	_, err = m.uploads.Finalize(ctx, user, v.UploadID, func(ctx context.Context, _ upload.SessionView, body io.Reader) error {
		var commitErr error
		v, commitErr = m.FinalizeReader(ctx, res, user, id, device, body)
		return commitErr
	})
	return v, err
}

// Cancel retains immutable identity and any durable original. Parts staging
// cancellation/release belongs to the parent transport; fallback uses Manager.
func (m *Manager) Cancel(ctx context.Context, res *filesvc.Resource, user users.User, id, device string) error {
	var uploadID string
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, id, device)
		if err != nil {
			return err
		}
		if op.Status == "publishing" && !tx.Catalog().BackupPublished(op.Plan.Files) {
			if err := tx.AbortBackup(op.Plan); err != nil {
				return err
			}
			src, err := loadSource(tx, op.SourceKey)
			if err != nil {
				return err
			}
			if src.Pending == op.ID {
				src.Pending = ""
				if err := tx.Write(op.SourceKey, src); err != nil {
					return err
				}
			}
		} else if err := recoverOperation(tx, &op); err != nil {
			return err
		}
		if op.Status == "stored" {
			return nil
		}
		op.Status = "cancelled"
		uploadID = op.UploadID
		return tx.Write(op.ID, op)
	})
	if err != nil {
		return err
	}
	if uploadID != "" && m.uploads != nil {
		return m.uploads.Cancel(user, uploadID)
	}
	return nil
}
