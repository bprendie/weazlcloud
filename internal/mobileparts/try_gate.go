package mobileparts

import "github.com/bprendie/weazlcloud/internal/filesvc"

// Background discovery must not wait for a client holding a session gate while
// streaming a part. Busy sessions retain durable markers for the next scan.
func (m *Manager) tryLock(res *filesvc.Resource, id string) (func(), bool) {
	key := keyFor(res, id)
	m.mu.Lock()
	g := m.gates[key]
	if g == nil {
		g = &sessionGate{}
		m.gates[key] = g
	}
	g.refs++
	m.mu.Unlock()
	if g.TryLock() {
		return func() { m.unlockGate(key, g) }, true
	}
	m.mu.Lock()
	g.refs--
	if g.refs == 0 {
		delete(m.gates, key)
		delete(m.checked, key)
	}
	m.mu.Unlock()
	return nil, false
}
