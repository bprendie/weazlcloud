package mobileparts

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

func queued(s Session) bool { return s.Status == "queued" || s.Status == "verifying" }
func live(s Session) bool   { return s.Status != "stored" && s.Status != "cancelled" }
func markerPath(res *filesvc.Resource, index, id string) string {
	return filepath.Join(Root(res), index, id)
}
func setMarker(res *filesvc.Resource, index, id string, exists bool) error {
	path := markerPath(res, index, id)
	dir := filepath.Dir(path)
	if !exists {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return syncDir(dir)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = syncDir(dir); err != nil {
		return err
	}
	return syncDir(Root(res))
}

// Additions precede the durable session write; removals follow it. A crash can
// leave a stale marker, but never hide a durably queued session.
func addSessionMarkers(res *filesvc.Resource, s Session) error {
	if live(s) {
		if err := setMarker(res, ".live", s.ID, true); err != nil {
			return err
		}
	}
	if queued(s) {
		return setMarker(res, ".queue", s.ID, true)
	}
	return nil
}
func syncSessionMarkers(res *filesvc.Resource, s Session) error {
	if err := setMarker(res, ".queue", s.ID, queued(s)); err != nil {
		return err
	}
	return setMarker(res, ".live", s.ID, live(s))
}
func dropSessionMarkers(res *filesvc.Resource, id string) error {
	if err := setMarker(res, ".queue", id, false); err != nil {
		return err
	}
	return setMarker(res, ".live", id, false)
}

// Recover at most 100 legacy headers per call, once per durable index upgrade.
// New sessions already have markers. Never load historical terminal receipts
// here: validation/indexing needs no payload cleanup or quota reservation.
func (m *Manager) recoverIndex(res *filesvc.Resource) error {
	complete := filepath.Join(Root(res), ".indexed-v1")
	if _, err := os.Stat(complete); err == nil {
		return m.recoverLive(res)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, eof, err := m.readDirectory(Root(res), 100)
	warnings := err
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !validID(id) {
			continue
		}
		if _, err = os.Stat(markerPath(res, ".live", id)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			warnings = errors.Join(warnings, err)
			continue
		}
		unlock, available := m.tryLock(res, id)
		if !available {
			// A legacy header may not have a marker yet. Preserve a recovery
			// candidate before completing the durable index upgrade.
			warnings = errors.Join(warnings, setMarker(res, ".live", id, true))
			continue
		}
		var s Session
		err = readSealed(res, filepath.Join(keyFor(res, id), "session.enc"), &s)
		if err == nil {
			err = validateSession(s, id)
		}
		if err == nil {
			err = syncSessionMarkers(res, s)
		}
		unlock()
		if errors.Is(err, ErrNotFound) {
			continue
		}
		warnings = errors.Join(warnings, err)
	}
	if eof && warnings == nil {
		warnings = setMarker(res, ".", ".indexed-v1", true)
	}
	return warnings
}
func (m *Manager) recoverLive(res *filesvc.Resource) error {
	entries, err := m.readMarkers(filepath.Join(Root(res), ".live"), 100)
	warnings := err
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
		if loadErr == nil {
			loadErr = syncSessionMarkers(res, s)
		}
		if errors.Is(loadErr, ErrNotFound) {
			loadErr = dropSessionMarkers(res, id)
		}
		if errors.Is(loadErr, ErrExpired) {
			loadErr = setMarker(res, ".queue", id, false)
		}
		unlock()
		warnings = errors.Join(warnings, loadErr)
	}
	return warnings
}
