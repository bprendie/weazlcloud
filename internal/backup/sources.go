package backup

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
)

func with(ctx context.Context, res *filesvc.Resource, user users.User, fn func(*library.BackupTransaction) error) error {
	if res == nil || res.Lib == nil || user.ID == "" || user.Disabled || user.Deleting {
		return ErrNotFound
	}
	return res.Lib.WithBackup(ctx, func(tx *library.BackupTransaction) error {
		if tx.OwnerID() != user.ID {
			return ErrNotFound
		}
		return fn(tx)
	})
}
func sourceKey(tx *library.BackupTransaction, device, id string) (string, error) {
	return tx.Key("source", []string{device, id})
}
func loadSource(tx *library.BackupTransaction, key string) (sourceRecord, error) {
	var src sourceRecord
	if err := tx.Read(key, &src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return src, ErrNotFound
		}
		return src, err
	}
	if src.Version < 0 || src.Version > 1 {
		return src, ErrUnsupportedVersion
	}
	src.Version = 1
	if src.Type != "source" {
		return src, ErrUnsupportedVersion
	}
	if src.Items == nil {
		src.Items = make(map[string]catalog.File)
	}
	return src, nil
}

// Register requires an existing owner folder ID. Retrying the same registration
// preserves mappings; changing its destination requires a revision-checked edit.
func (m *Manager) Register(ctx context.Context, res *filesvc.Resource, user users.User, device string, src Source) (Source, error) {
	var out Source
	if device != "" {
		if src.DeviceID != "" && src.DeviceID != device {
			return out, ErrNotFound
		}
		src.DeviceID = device
	}
	if !opaque(src.ID) || !opaque(src.DeviceID) || !opaque(src.Name) || src.DestinationID == "" {
		return out, ErrInvalid
	}
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		key, err := sourceKey(tx, src.DeviceID, src.ID)
		if err != nil {
			return err
		}
		existing, err := loadSource(tx, key)
		if err == nil {
			if existing.Source.DestinationID != src.DestinationID || existing.Source.Name != src.Name {
				return ErrIdempotencyConflict
			}
			out = existing.Source
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var dest catalog.File
		for _, f := range tx.Catalog().List() {
			if f.EntryID == src.DestinationID && f.Folder {
				dest = f
				break
			}
		}
		if dest.EntryID == "" {
			return ErrNotFound
		}
		src.Status = "active"
		src.Revision = 1
		record := sourceRecord{Version: 1, Type: "source", Source: src, Destination: dest, Items: make(map[string]catalog.File)}
		if err := tx.WriteAuthorized(key, record); err != nil {
			return err
		}
		out = src
		return nil
	})
	return out, err
}

func (m *Manager) UpdateSource(ctx context.Context, res *filesvc.Resource, user users.User, device, id, status string, revision uint64) (Source, error) {
	var out Source
	if !opaque(device) || !opaque(id) || status != "active" && status != "paused" && status != "detached" {
		return out, ErrInvalid
	}
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		key, err := sourceKey(tx, device, id)
		if err != nil {
			return err
		}
		src, err := loadSource(tx, key)
		if err != nil {
			return err
		}
		if src.Source.Revision != revision || revision == ^uint64(0) {
			return ErrStale
		}
		if src.Source.Status == "detached" && status != "detached" {
			return ErrPaused
		}
		src.Source.Status = status
		src.Source.Revision++
		if err := tx.WriteAuthorized(key, src); err != nil {
			return err
		}
		out = src.Source
		return nil
	})
	return out, err
}

// Sources returns at most 200 own-device registrations, in stable opaque order.
func (m *Manager) Sources(ctx context.Context, res *filesvc.Resource, user users.User, device, after string) ([]Source, string, error) {
	out := []Source{}
	next := ""
	if !opaque(device) {
		return out, next, ErrInvalid
	}
	err := m.with(ctx, res, user, func(tx *library.BackupTransaction) error {
		keys, err := tx.Keys()
		if err != nil {
			return err
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key <= after {
				continue
			}
			var src sourceRecord
			if err := tx.Read(key, &src); err != nil {
				return err
			}
			if src.Type == "source" && src.Source.DeviceID == device && (src.Version < 0 || src.Version > 1) {
				return ErrUnsupportedVersion
			}
			if src.Type != "source" || src.Source.DeviceID != device {
				continue
			}
			if len(out) == 200 {
				next = keysCursor(keys, key)
				break
			}
			out = append(out, src.Source)
		}
		return nil
	})
	return out, next, err
}
func keysCursor(keys []string, key string) string {
	for i, k := range keys {
		if k == key && i > 0 {
			return keys[i-1]
		}
	}
	return ""
}

func (m *Manager) with(ctx context.Context, res *filesvc.Resource, user users.User, fn func(*library.BackupTransaction) error) error {
	return with(ctx, res, user, func(tx *library.BackupTransaction) error { tx.SetCommitGuard(m.commitGuard); return fn(tx) })
}
