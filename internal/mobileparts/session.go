package mobileparts

import "errors"

import "github.com/bprendie/weazlcloud/internal/filesvc"

// Session reads private, vault-encrypted metadata under the session gate.
// Callers must check owner, kind and current device/grant authorization; this
// metadata (including the staging key) must never be returned in HTTP responses.
func (m *Manager) Session(res *filesvc.Resource, id string) (Session, error) {
	unlock := m.lock(res, id)
	defer unlock()
	return m.load(res, id)
}

// StatusKind projects one owner/device/kind checked snapshot without returning
// private keys or authorization payloads, including for expired foreign IDs.
func (m *Manager) StatusKind(res *filesvc.Resource, id, owner, device, kind string) (View, error) {
	unlock := m.lock(res, id)
	defer unlock()
	s, e := m.load(res, id)
	if (e == nil || errors.Is(e, ErrExpired)) && (s.OwnerID != owner || s.Spec.DeviceID != device || s.Spec.Kind != kind) {
		return View{}, ErrNotFound
	}
	if e != nil {
		return View{}, e
	}
	return view(s), nil
}
