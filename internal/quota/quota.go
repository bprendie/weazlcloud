package quota

import (
	"errors"
	"io"
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
	mu       sync.Mutex
	root     string
	reserved uint64
	statfs   func(string) (uint64, uint64, error)
}

func New(root string) *Manager {
	return &Manager{root: root, statfs: func(path string) (uint64, uint64, error) {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err != nil {
			return 0, 0, err
		}
		return fs.Blocks * uint64(fs.Bsize), fs.Bavail * uint64(fs.Bsize), nil
	}}
}

func newWithStatfs(root string, statfs func(string) (uint64, uint64, error)) *Manager {
	return &Manager{root: root, statfs: statfs}
}

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
	// The incoming bytes first exist as a temporary spool and then as restic
	// pack data. Reserve the full incoming size even for an overwrite so the
	// write has room for both copies while it is being committed.
	delta := incoming
	// All approved users share the same usable node storage. The user
	// arguments remain in the method signature for callers during migration,
	// but there is deliberately no per-user allocation or cap.
	// Filesystem usage includes existing restic packs and metadata. The write
	// reservation is conservative: it prevents a request from crossing the
	// configured ceiling before restic has added its pack overhead.
	if s.Used >= limit || m.reserved > limit-s.Used || uint64(delta) > limit-s.Used-m.reserved {
		return nil, ErrExceeded
	}
	m.reserved += uint64(delta)
	released := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		m.reserved -= uint64(delta)
	}, nil
}

// GuardReader reserves known request bytes before they are spooled. For a
// request without Content-Length it reserves each chunk after it is read and
// holds those reservations until the write finishes.
func (m *Manager) GuardReader(userID string, users int, userUsed, current, expected int64, src io.Reader) (io.Reader, func(), error) {
	if expected >= 0 {
		release, err := m.Reserve(userID, users, userUsed, current, expected)
		return src, release, err
	}
	g := &guardedReader{manager: m, userID: userID, users: users, userUsed: userUsed, current: current, src: src}
	return g, g.release, nil
}

type guardedReader struct {
	manager           *Manager
	userID            string
	users             int
	userUsed, current int64
	src               io.Reader
	mu                sync.Mutex
	releases          []func()
	released          bool
}

func (g *guardedReader) Read(p []byte) (int, error) {
	n, err := g.src.Read(p)
	if n > 0 {
		release, reserveErr := g.manager.Reserve(g.userID, g.users, g.userUsed, g.current, int64(n))
		if reserveErr != nil {
			return 0, reserveErr
		}
		g.mu.Lock()
		if g.released {
			g.mu.Unlock()
			release()
			return 0, errors.New("quota reservation closed")
		}
		g.releases = append(g.releases, release)
		g.mu.Unlock()
	}
	return n, err
}

func (g *guardedReader) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return
	}
	g.released = true
	for _, release := range g.releases {
		release()
	}
	g.releases = nil
}

func (m *Manager) status(users int) (Status, error) {
	capacity, free, err := m.statfs(m.root)
	if err != nil {
		return Status{}, err
	}
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
