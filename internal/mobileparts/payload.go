package mobileparts

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

// RebindPayload replaces private authorization metadata using an exact-byte CAS.
// The caller must validate identical immutable content and a fresh grant for the
// same owner/device. Expired inactive sessions restart with fresh encrypted staging
// after quota admission. Payloads and staging keys remain private to the server.
func (m *Manager) RebindPayload(res *filesvc.Resource, id, device string, oldPayload, newPayload json.RawMessage) error {
	if len(newPayload) > 65536 || !json.Valid(newPayload) {
		return ErrInvalid
	}
	replacement := append(json.RawMessage(nil), newPayload...)
	unlock := m.lock(res, id)
	defer unlock()
	s, err := m.load(res, id)
	expired := errors.Is(err, ErrExpired)
	if err != nil && !expired {
		return err
	}
	if s.Spec.DeviceID != device {
		return ErrNotFound
	}
	if (s.Status == "verifying" && !expired) || s.Status == "stored" || s.Status == "cancelled" || m.isActive(res, id) || !bytes.Equal(s.Spec.Payload, oldPayload) {
		return ErrConflict
	}
	s.Spec.Payload = replacement
	if expired {
		return m.restartExpired(res, &s)
	}
	if s.Status == "failed" && ready(s) && s.Spec.CommitWhenComplete {
		s.Status, s.ErrorCode = "queued", ""
	}
	return m.save(res, &s)
}

func (m *Manager) restartExpired(res *filesvc.Resource, s *Session) error {
	key, err := cryptox.Random(32)
	if err != nil {
		return err
	}
	defer clear(key)
	s.Key = cryptox.B64(key)
	s.Status, s.ErrorCode, s.Result = "uploading", "", nil
	s.RetryAttempts, s.RetryAfter = 0, time.Time{}
	for i := range s.Spec.Components {
		s.Spec.Components[i].ReceivedParts, s.Spec.Components[i].ReceivedBytes = 0, 0
	}
	if ready(*s) && s.Spec.CommitWhenComplete {
		s.Status = "queued"
	}
	// Existing payload still counts in filesystem usage. Obtain all remaining
	// staging and commit workspace before deleting any expired bytes.
	if err = m.reserve(res, *s); err != nil {
		return err
	}
	if err = m.removePayload(res, s.ID); err == nil {
		err = m.save(res, s)
	}
	if err != nil {
		m.release(res, s.ID)
	}
	return err
}
