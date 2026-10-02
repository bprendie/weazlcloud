package mobileparts

import (
	"context"
	"math"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
)

type reservation struct {
	owner string
	lease *quota.Reservation
}

// reservationBytes excludes persisted parts already counted by filesystem
// usage, but retains commit source/destination and shared index workspace.
func reservationBytes(s Session) (int64, error) {
	var total int64 = 8192
	for _, c := range s.Spec.Components {
		if c.ReceivedBytes < 0 || c.ReceivedBytes > c.Size || c.ReceivedParts < 0 || c.ReceivedParts > count(c) {
			return 0, ErrCorrupt
		}
		workspace, err := quota.SharedWriteReservation(c.Size, 0)
		if err != nil {
			return 0, err
		}
		for _, n := range []int64{workspace, c.Size - c.ReceivedBytes, (count(c) - c.ReceivedParts) * 8192} {
			if n < 0 || total > math.MaxInt64-n {
				return 0, quota.ErrExceeded
			}
			total += n
		}
	}
	return total, nil
}
func (m *Manager) reserve(res *filesvc.Resource, s Session) error {
	key := keyFor(res, s.ID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if g := m.gates[key]; g != nil {
		g.owner = s.OwnerID
	}
	if s.Status == "stored" || s.Status == "cancelled" {
		if r, ok := m.reservations[key]; ok {
			r.lease.Release()
			delete(m.reservations, key)
		}
		return nil
	}
	if m.draining[s.OwnerID] > 0 {
		if m.active[key] != nil {
			return nil
		}
		return ErrConflict
	}
	if m.quota == nil {
		return nil
	}
	n, err := reservationBytes(s)
	if err != nil {
		return err
	}
	if r, ok := m.reservations[key]; ok {
		return r.lease.Resize(n)
	}
	r, err := m.quota.ReserveTracked(s.OwnerID, 1, 0, 0, n)
	if err != nil {
		return err
	}
	m.reservations[key] = reservation{s.OwnerID, r}
	return nil
}
func (m *Manager) release(res *filesvc.Resource, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := keyFor(res, id)
	if r, ok := m.reservations[key]; ok {
		r.lease.Release()
		delete(m.reservations, key)
	}
	delete(m.checked, key)
}

type activeJob struct {
	owner  string
	cancel context.CancelFunc
	done   chan struct{}
}

// ReleaseOwner cancels and drains workers and in-flight session operations,
// then releases quota leases. Call before locking/evicting the owner's resource.
// Durable encrypted sessions survive and reserve again on the next load.
func (m *Manager) ReleaseOwner(owner string) error {
	m.mu.Lock()
	m.draining[owner]++
	var jobs []*activeJob
	gates := map[string]*sessionGate{}
	for _, job := range m.active {
		if job.owner == owner {
			job.cancel()
			jobs = append(jobs, job)
		}
	}
	for key, g := range m.gates {
		if g.owner == owner {
			g.refs++
			gates[key] = g
		}
	}
	m.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
	for key, g := range gates {
		g.Lock()
		m.unlockGate(key, g)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, r := range m.reservations {
		if r.owner == owner {
			r.lease.Release()
			delete(m.reservations, key)
			delete(m.checked, key)
		}
	}
	m.draining[owner]--
	if m.draining[owner] == 0 {
		delete(m.draining, owner)
	}
	return nil
}
func (m *Manager) isActive(res *filesvc.Resource, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[keyFor(res, id)] != nil
}
