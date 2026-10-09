package mobileparts

import (
	"encoding/json"
	"errors"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

// CancelCoordinated serializes cleanup with admission/finalization. Cleanup must
// never call the engine: it durably cancels the coordinator first, or supplies
// a non-nil JSON result proving publication already completed. Errors preserve
// the engine's state, permitting an idempotent retry after coordinator recovery.
func (m *Manager) CancelCoordinated(res *filesvc.Resource, id, device string, cleanup func(Session) (json.RawMessage, error)) (View, error) {
	resume, err := m.stopReceivers(res, id, device)
	if err != nil {
		return View{}, err
	}
	defer resume()
	unlock := m.lock(res, id)
	defer unlock()
	s, err := m.load(res, id)
	if err != nil && !errors.Is(err, ErrExpired) {
		return View{}, err
	}
	if s.Spec.DeviceID != device {
		return View{}, ErrNotFound
	}
	if s.Status == "stored" || m.isActive(res, id) {
		return View{}, ErrConflict
	}
	var result json.RawMessage
	if cleanup != nil {
		result, err = cleanup(s)
		if err != nil {
			return View{}, err
		}
		if result != nil && !json.Valid(result) {
			return View{}, ErrInvalid
		}
	}
	s.Status, s.ErrorCode, s.Result = "cancelled", "", nil
	if result != nil {
		s.Status, s.Result = "stored", append(json.RawMessage(nil), result...)
	}
	if err = m.save(res, &s); err != nil {
		return View{}, err
	}
	err = m.removePayload(res, id)
	m.release(res, id)
	return view(s), err
}
