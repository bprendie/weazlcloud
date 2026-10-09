package mobileparts

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

// A receiver owns only an encrypted temporary file until its short commit.
// Session gates never cover network I/O. Cleanup blocks new receivers and joins
// existing writers before removing payloads or releasing reservations.
type receiver struct {
	owner, session string
	cancel         context.CancelFunc
	done           chan struct{}
}

type ReceiveLimits struct {
	Global, PerOwner, Pending int
	PendingBytes              int64
}

func (m *Manager) ConfigureReceive(l ReceiveLimits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiveLimits = l
}
func (m *Manager) ReceiveLimits() ReceiveLimits {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.receiveLimits
}
func (m *Manager) beginReceive(ctx context.Context, res *filesvc.Resource, s Session, src io.Reader) (context.Context, func(), error) {
	m.mu.Lock()
	key := keyFor(res, s.ID)
	n := 0
	for r := range m.receivers {
		if r.owner == s.OwnerID {
			n++
		}
	}
	if m.draining[s.OwnerID] > 0 || m.receiveBlocked[key] > 0 {
		m.mu.Unlock()
		return ctx, nil, ErrConflict
	}
	if m.active[key] != nil {
		m.mu.Unlock()
		return ctx, nil, ErrBusy
	}
	if len(m.receivers) >= m.receiveLimits.Global || n >= m.receiveLimits.PerOwner {
		m.mu.Unlock()
		return ctx, nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &receiver{owner: s.OwnerID, session: key, cancel: cancel, done: make(chan struct{})}
	m.receivers[r] = true
	receiveActive.Add(1)
	m.mu.Unlock()
	// Network readers support Close. Closing unblocks a cancelled body Read.
	var closeOnce sync.Once
	closeBody := func() {
		if c, ok := src.(io.Closer); ok {
			closeOnce.Do(func() { _ = c.Close() })
		}
	}
	stop := context.AfterFunc(ctx, closeBody)
	return ctx, func() {
		stop()
		cancel()
		m.mu.Lock()
		delete(m.receivers, r)
		receiveActive.Add(-1)
		close(r.done)
		if g := m.gates[key]; g == nil {
			delete(m.checked, key)
		}
		m.mu.Unlock()
	}, nil
}
func (m *Manager) hasReceiversLocked(key string) bool {
	for r := range m.receivers {
		if r.session == key {
			return true
		}
	}
	return false
}
func (m *Manager) stopReceivers(res *filesvc.Resource, id, device string) (func(), error) {
	unlock := m.lock(res, id)
	s, err := m.load(res, id)
	if err != nil && !errors.Is(err, ErrExpired) {
		unlock()
		return nil, err
	}
	if s.Spec.DeviceID != device {
		unlock()
		return nil, ErrNotFound
	}
	key := keyFor(res, id)
	m.mu.Lock()
	m.receiveBlocked[key]++
	var jobs []*receiver
	for r := range m.receivers {
		if r.session == key {
			r.cancel()
			jobs = append(jobs, r)
		}
	}
	m.mu.Unlock()
	unlock()
	for _, r := range jobs {
		<-r.done
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.receiveBlocked[key]--
		if m.receiveBlocked[key] == 0 {
			delete(m.receiveBlocked, key)
		}
	}, nil
}
