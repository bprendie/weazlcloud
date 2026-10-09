package mobileparts

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Pending reads at most 100 queue markers plus 100 incremental recovery entries.
// Historical stored/cancelled receipts never enter the steady-state queue scan.
// Valid jobs are returned alongside joined warnings for other candidates.
func (m *Manager) Pending(res *filesvc.Resource) ([]Session, error) {
	if !res.Vault.Unlocked() {
		return nil, vault.ErrLocked
	}
	warnings := m.recoverIndex(res)
	entries, err := m.readMarkers(filepath.Join(Root(res), ".queue"), 100)
	warnings = errors.Join(warnings, err)
	out := make([]Session, 0, len(entries))
	for _, entry := range entries {
		id := entry.Name()
		if !validID(id) || entry.IsDir() {
			continue
		}
		unlock, available := m.tryLock(res, id)
		if !available {
			continue
		}
		s, loadErr := m.load(res, id)
		if loadErr == nil && queued(s) && !m.now().Before(s.RetryAfter) {
			out = append(out, s)
		}
		if loadErr == nil && !queued(s) || errors.Is(loadErr, ErrExpired) || errors.Is(loadErr, ErrNotFound) {
			loadErr = setMarker(res, ".queue", id, false)
		}
		unlock()
		warnings = errors.Join(warnings, loadErr)
	}
	return out, warnings
}

// Sweep scans at most 100 live markers plus 100 recovery entries. Terminal
// tombstones remain durable; headers are validated before expiry deletion.
func (m *Manager) Sweep(res *filesvc.Resource) error {
	if !res.Vault.Unlocked() {
		return vault.ErrLocked
	}
	warnings := m.recoverIndex(res)
	entries, err := m.readMarkers(filepath.Join(Root(res), ".live"), 100)
	warnings = errors.Join(warnings, err)
	for _, entry := range entries {
		id := entry.Name()
		if !validID(id) || entry.IsDir() {
			continue
		}
		unlock, available := m.tryLock(res, id)
		if !available {
			continue
		}
		if m.isActive(res, id) {
			unlock()
			continue
		}
		var s Session
		err = readSealed(res, filepath.Join(keyFor(res, id), "session.enc"), &s)
		if err == nil {
			err = validateSession(s, id)
		}
		if err == nil && live(s) && m.now().Sub(s.UpdatedAt) >= Lifetime {
			err = os.RemoveAll(keyFor(res, id))
			if err == nil {
				m.release(res, id)
				err = syncDir(Root(res))
			}
			if err == nil {
				err = dropSessionMarkers(res, id)
			}
		} else if err == nil {
			err = syncSessionMarkers(res, s)
		}
		if errors.Is(err, ErrNotFound) {
			err = dropSessionMarkers(res, id)
		}
		unlock()
		warnings = errors.Join(warnings, err)
	}
	return warnings
}
