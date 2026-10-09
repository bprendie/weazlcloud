package mobileparts

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"time"
)

// A context-aware acquisition uses the existing mutex and reference discipline.
// No goroutine waits beyond the request lifetime. Network I/O never owns it.
func (m *Manager) lockContext(ctx context.Context, res *filesvc.Resource, id string) (func(), error) {
	key := keyFor(res, id)
	m.mu.Lock()
	g := m.gates[key]
	if g == nil {
		g = &sessionGate{}
		m.gates[key] = g
	}
	g.refs++
	m.mu.Unlock()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := ctx.Err(); e != nil {
			m.mu.Lock()
			g.refs--
			if g.refs == 0 {
				delete(m.gates, key)
				if !m.hasReceiversLocked(key) {
					delete(m.checked, key)
				}
			}
			m.mu.Unlock()
			return nil, e
		}
		if g.TryLock() {
			return func() { m.unlockGate(key, g) }, nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
