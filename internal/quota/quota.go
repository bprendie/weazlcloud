package quota

import (
	"errors"
	"math"
	"sync"
	"syscall"
)

const HardPercent uint64 = 97

var ErrExceeded = errors.New("quota exceeded")

type Status struct {
	Capacity uint64 `json:"capacity"`
	Used     uint64 `json:"used"`
	Limit    uint64 `json:"limit"`
	Percent  int    `json:"percent"`
	Users    int    `json:"users"`
}

type Manager struct {
	mu           sync.Mutex
	root         string
	reserved     uint64
	userReserved map[string]uint64
}

func New(root string) *Manager { return &Manager{root: root, userReserved: make(map[string]uint64)} }

func (m *Manager) Status(users int) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status(users)
}

func (m *Manager) Check(users int, userUsed, current, incoming int64) error {
	release, err := m.Reserve("", users, userUsed, current, incoming)
	if err != nil {
		return err
	}
	release()
	return nil
}

func (m *Manager) Reserve(userID string, users int, userUsed, current, incoming int64) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if incoming < 0 || current < 0 || userUsed < 0 {
		return nil, ErrExceeded
	}
	s, err := m.status(users)
	if err != nil {
		return nil, err
	}
	limit := s.Limit
	delta := incoming
	if current > 0 && incoming > current {
		delta = incoming - current
	} else if current > 0 {
		delta = 0
	}
	if users > 0 {
		perUser := limit / uint64(users)
		if uint64(userUsed)+m.userReserved[userID]+uint64(delta) > perUser {
			return nil, ErrExceeded
		}
	}
	// Filesystem usage includes existing restic packs and metadata. The write
	// reservation is conservative: it prevents a request from crossing the
	// configured ceiling before restic has added its pack overhead.
	if s.Used >= limit || m.reserved > limit-s.Used || uint64(delta) > limit-s.Used-m.reserved {
		return nil, ErrExceeded
	}
	m.reserved += uint64(delta)
	m.userReserved[userID] += uint64(delta)
	released := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		m.reserved -= uint64(delta)
		m.userReserved[userID] -= uint64(delta)
	}, nil
}

func (m *Manager) status(users int) (Status, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(m.root, &fs); err != nil {
		return Status{}, err
	}
	capacity := fs.Blocks * uint64(fs.Bsize)
	free := fs.Bavail * uint64(fs.Bsize)
	used := uint64(0)
	if capacity > free {
		used = capacity - free
	}
	limit := capacity * HardPercent / 100
	percent := 0
	if limit > 0 {
		percent = int(math.Min(100, float64(used)*100/float64(limit)))
	}
	return Status{Capacity: capacity, Used: used, Limit: limit, Percent: percent, Users: users}, nil
}
