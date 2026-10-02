package mobileparts

import "github.com/bprendie/weazlcloud/internal/filesvc"

// Session reads private, vault-encrypted metadata under the session gate.
// Callers must check owner, kind and current device/grant authorization; this
// metadata (including the staging key) must never be returned in HTTP responses.
func (m *Manager) Session(res *filesvc.Resource, id string) (Session, error) {
	unlock := m.lock(res, id)
	defer unlock()
	return m.load(res, id)
}
