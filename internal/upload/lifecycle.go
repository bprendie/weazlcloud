package upload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (m *Manager) reconcileAll() {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return
	}
	for _, owner := range entries {
		if !owner.IsDir() || !validComponent(owner.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(m.root, owner.Name()))
		if err != nil {
			continue
		}
		for _, entry := range files {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			m.mu.Lock()
			if s, loadErr := m.loadLocked(owner.Name(), id); loadErr == nil && s.Status == "ready" {
				_ = m.writeLocked(s)
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) Reserve(owner users.User, bytes, current int64, res *filesvc.Resource) (func(), error) {
	if m.quota == nil {
		return func() {}, nil
	}
	count := 1
	if m.userCount != nil {
		count = m.userCount()
	}
	reservation, err := m.quota.ReserveTracked(owner.ID, count, 0, current, bytes)
	if err != nil {
		return nil, err
	}
	return reservation.Release, nil
}

func (m *Manager) reserveBytes(owner string, bytes int64) (*quota.Reservation, error) {
	count := 1
	if m.userCount != nil {
		count = m.userCount()
	}
	return m.quota.ReserveTracked(owner, count, 0, 0, bytes)
}

func (m *Manager) releaseReservationLocked(id string) {
	if reservation := m.reservations[id]; reservation != nil {
		reservation.Release()
		delete(m.reservations, id)
	}
	delete(m.reservationErrors, id)
}

func (m *Manager) ensureReservationLocked(s session) error {
	if m.quota == nil || s.Status == "complete" {
		return nil
	}
	if err := m.reservationErrors[s.ID]; err != nil {
		return err
	}
	if m.reservations[s.ID] != nil {
		return nil
	}
	reservation, err := m.reserveBytes(s.OwnerID, 2*s.Size-s.Offset)
	if err != nil {
		m.reservationErrors[s.ID] = err
		return err
	}
	m.reservations[s.ID] = reservation
	return nil
}

func (m *Manager) restoreReservations() {
	entries, err := os.ReadDir(m.root)
	if err != nil || m.quota == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, owner := range entries {
		if !owner.IsDir() || !validComponent(owner.Name()) {
			continue
		}
		files, readErr := os.ReadDir(m.ownerDir(owner.Name()))
		if readErr != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".json")
			s, loadErr := m.loadLocked(owner.Name(), id)
			if loadErr != nil || s.Status == "complete" {
				continue
			}
			if err := m.ensureReservationLocked(s); err != nil {
				m.reservationErrors[id] = err
			}
		}
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.SweepExpired(now.UTC())
		}
	}
}

func (m *Manager) SweepExpired(now time.Time) int {
	removed, _, _ := m.SweepExpiredDetailed(context.Background(), now)
	return removed
}

func (m *Manager) Resource(owner users.User) *filesvc.Resource {
	if m.resourceFor == nil {
		return nil
	}
	return m.resourceFor(owner)
}
