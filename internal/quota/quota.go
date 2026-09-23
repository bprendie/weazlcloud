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
	Reserved uint64 `json:"reserved"`
	Percent  int    `json:"percent"`
	Users    int    `json:"users"`
}

type Manager struct {
	mu       sync.Mutex
	root     string
	reserved uint64
	statfs   func(string) (uint64, uint64, error)
}

type Reservation struct {
	manager  *Manager
	bytes    uint64
	released bool
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
	r, err := m.ReserveTracked(userID, users, userUsed, current, incoming)
	if err != nil {
		return nil, err
	}
	return r.Release, nil
}

func (m *Manager) ReserveTracked(userID string, users int, userUsed, current, incoming int64) (*Reservation, error) {
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
	return &Reservation{manager: m, bytes: uint64(delta)}, nil
}

func (r *Reservation) Resize(bytes int64) error {
	if bytes < 0 {
		return ErrExceeded
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.released {
		return ErrExceeded
	}
	s, err := m.status(1)
	if err != nil {
		return err
	}
	next := uint64(bytes)
	without := m.reserved - r.bytes
	if s.Used >= s.Limit || without > s.Limit-s.Used || next > s.Limit-s.Used-without {
		return ErrExceeded
	}
	m.reserved = without + next
	r.bytes = next
	return nil
}

func (r *Reservation) Release() {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	m.reserved -= r.bytes
	r.bytes = 0
}

// GuardReader reserves known request bytes before they are spooled. For a
// request without Content-Length it reserves each chunk after it is read and
// holds those reservations until the write finishes.
func (m *Manager) GuardReader(userID string, users int, userUsed, current, expected int64, src io.Reader) (io.Reader, func(), error) {
	return m.GuardReaderMultiplier(userID, users, userUsed, current, expected, 1, src)
}

// GuardReaderMultiplier reserves a conservative multiple of incoming bytes
// when a backend temporarily keeps source and encrypted destination copies.
func (m *Manager) GuardReaderMultiplier(userID string, users int, userUsed, current, expected, multiplier int64, src io.Reader) (io.Reader, func(), error) {
	return m.GuardReaderWorkspace(userID, users, userUsed, current, expected, multiplier, 0, 0, 0, src)
}

// GuardReaderWorkspace also reserves fixed and per-chunk index/manifest overhead.
func (m *Manager) GuardReaderWorkspace(userID string, users int, userUsed, current, expected, multiplier, fixed, perChunk, chunkSize int64, src io.Reader) (io.Reader, func(), error) {
	if multiplier < 1 || (expected >= 0 && expected > math.MaxInt64/multiplier) {
		return nil, nil, ErrExceeded
	}
	if fixed < 0 || perChunk < 0 || (perChunk > 0 && chunkSize <= 0) {
		return nil, nil, ErrExceeded
	}
	if expected >= 0 {
		overhead, err := workspaceOverhead(expected, fixed, perChunk, chunkSize)
		if err != nil || expected*multiplier > math.MaxInt64-overhead {
			return nil, nil, ErrExceeded
		}
		release, err := m.Reserve(userID, users, userUsed, current, expected*multiplier+overhead)
		return src, release, err
	}
	var releases []func()
	if fixed > 0 {
		release, err := m.Reserve(userID, users, userUsed, current, fixed)
		if err != nil {
			return nil, nil, err
		}
		releases = append(releases, release)
	}
	g := &guardedReader{manager: m, userID: userID, users: users, userUsed: userUsed, current: current, multiplier: multiplier, fixed: fixed, perChunk: perChunk, chunkSize: chunkSize, releases: releases, src: src}
	return g, g.release, nil
}

type guardedReader struct {
	manager           *Manager
	userID            string
	users             int
	userUsed, current int64
	multiplier        int64
	fixed, perChunk   int64
	chunkSize, read   int64
	src               io.Reader
	mu                sync.Mutex
	releases          []func()
	released          bool
}

func (g *guardedReader) Read(p []byte) (int, error) {
	n, err := g.src.Read(p)
	if n > 0 {
		amount := int64(n)
		if amount > math.MaxInt64/g.multiplier || g.read > math.MaxInt64-amount {
			return 0, ErrExceeded
		}
		newRead := g.read + amount
		extra := int64(0)
		if g.perChunk > 0 {
			oldChunks := workspaceChunks(g.read, g.chunkSize)
			newChunks := workspaceChunks(newRead, g.chunkSize)
			if newChunks-oldChunks > math.MaxInt64/g.perChunk {
				return 0, ErrExceeded
			}
			extra = (newChunks - oldChunks) * g.perChunk
		}
		reserve := amount * g.multiplier
		if reserve > math.MaxInt64-extra {
			return 0, ErrExceeded
		}
		release, reserveErr := g.manager.Reserve(g.userID, g.users, g.userUsed, g.current, reserve+extra)
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
		g.read = newRead
		g.mu.Unlock()
	}
	return n, err
}

func workspaceChunks(size, chunkSize int64) int64 {
	if size == 0 {
		return 0
	}
	return (size-1)/chunkSize + 1
}

func workspaceOverhead(size, fixed, perChunk, chunkSize int64) (int64, error) {
	if perChunk == 0 {
		return fixed, nil
	}
	chunks := workspaceChunks(size, chunkSize)
	if chunks > (math.MaxInt64-fixed)/perChunk {
		return 0, ErrExceeded
	}
	return fixed + chunks*perChunk, nil
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
	return Status{Capacity: capacity, Used: used, Limit: limit, Reserved: m.reserved, Percent: percent, Users: users}, nil
}
