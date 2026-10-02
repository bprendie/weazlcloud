package backup

import (
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
)

func operationKey(tx *library.BackupTransaction, spec Spec) (string, error) {
	return tx.Key("operation", []string{spec.DeviceID, spec.SourceID, spec.ItemID, spec.SourceRevision})
}
func loadOperation(tx *library.BackupTransaction, id, device string) (operation, error) {
	var op operation
	if err := tx.Read(id, &op); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return op, ErrNotFound
		}
		return op, err
	}
	if op.Type != "operation" || op.ID != id {
		return op, ErrUnsupportedVersion
	}
	if device != "" && op.Spec.DeviceID != device {
		return op, ErrNotFound
	}
	if op.Version < 0 || op.Version > 1 {
		return op, ErrUnsupportedVersion
	}
	op.Version = 1
	return op, nil
}

// CreateParts records an immutable source-revision intent without creating a
// legacy upload session or plaintext payload. The parent owns parts staging.
func (m *Manager) CreateParts(ctx context.Context, res *filesvc.Resource, user users.User, device string, spec Spec) (View, error) {
	if spec.Transport != "" && spec.Transport != "parts-v1" {
		return View{}, ErrInvalid
	}
	spec.Transport = "parts-v1"
	return m.Create(ctx, res, user, device, spec)
}
func (m *Manager) Create(ctx context.Context, res *filesvc.Resource, user users.User, device string, spec Spec) (View, error) {
	spec, err := normalize(spec, device)
	if err != nil {
		return View{}, err
	}
	var out View
	err = m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		id, err := operationKey(tx, spec)
		if err != nil {
			return err
		}
		if op, err := loadOperation(tx, id, spec.DeviceID); err == nil {
			if !reflect.DeepEqual(op.Spec, spec) {
				return ErrIdempotencyConflict
			}
			if err := recoverOperation(tx, &op); err != nil {
				return err
			}
			out = op.View
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		key, err := sourceKey(tx, spec.DeviceID, spec.SourceID)
		if err != nil {
			return err
		}
		src, err := loadSource(tx, key)
		if err != nil {
			return err
		}
		if err := recoverSource(tx, &src, key); err != nil {
			return err
		}
		if src.Source.Status != "active" {
			return ErrPaused
		}
		plan, primary, err := planItem(tx, src, spec)
		if err != nil {
			return err
		}
		op := operation{Version: 1, Type: "operation", View: View{ID: id, ReceiptID: id, Status: "accepted", Spec: spec}, Plan: plan, SourceKey: key, PrimaryID: primary}
		// Persist intent before creating fallback staging. A failed staging creation
		// can be retried from this intent without generating a new receipt identity.
		if err := tx.WriteAuthorized(id, op); err != nil {
			return err
		}
		out = op.View
		return nil
	})
	if err != nil {
		return out, err
	}
	if out.Spec.Transport == "sequential" && out.Spec.Kind == "file" && out.Status == "accepted" {
		out, err = m.ensureSequential(ctx, res, user, out)
	}
	return out, err
}

// Creating upload.Manager staging outside the library lock avoids its quota
// callbacks recursively entering Library. The deterministic key makes concurrent
// and interrupted creation converge on one session.
func (m *Manager) ensureSequential(ctx context.Context, res *filesvc.Resource, user users.User, v View) (View, error) {
	if m.uploads == nil {
		return v, ErrInvalid
	}
	session, err := m.uploads.CreateIdempotent(user, "backup-"+v.ID, v.Spec.Size, v.Spec.SHA256, "backup:"+v.ID)
	if err != nil {
		return v, err
	}
	err = m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, v.ID, v.Spec.DeviceID)
		if err != nil {
			return err
		}
		if op.Status == "accepted" {
			op.UploadID = session.ID
			if err := tx.Write(op.ID, op); err != nil {
				return err
			}
		}
		v = op.View
		return nil
	})
	return v, err
}
