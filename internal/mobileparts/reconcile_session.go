package mobileparts

import "github.com/bprendie/weazlcloud/internal/filesvc"

// Polls may repair counters and markers without rewriting unchanged headers.
func (m *Manager) recoverSession(res *filesvc.Resource, s *Session) error {
	before := append([]Component(nil), s.Spec.Components...)
	status := s.Status
	if err := m.reconcile(res, s); err != nil {
		return err
	}
	changed := status != s.Status
	for i, c := range before {
		if c != s.Spec.Components[i] {
			changed = true
		}
	}
	if changed {
		return m.persist(res, s)
	}
	if err := syncSessionMarkers(res, *s); err != nil {
		return err
	}
	m.mu.Lock()
	m.checked[keyFor(res, s.ID)] = s.UpdatedAt
	m.mu.Unlock()
	return nil
}
