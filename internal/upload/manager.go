package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

const MaxChunkBytes int64 = 16 << 20
const BrowserChunkBytes int64 = 8 << 20
const SessionLifetime = 24 * time.Hour
const SweepInterval = 15 * time.Minute

type SessionView struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	Offset      int64     `json:"offset"`
	Hash        string    `json:"hash,omitempty"`
	ChunkHashes []string  `json:"chunk_hashes,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DeleteOwner waits for all in-flight session operations before removing the
// owner's upload manifests, payloads, chunks, and quota reservations.
func (m *Manager) DeleteOwner(owner string) error {
	if !validComponent(owner) {
		return ErrNotFound
	}
	entries, err := os.ReadDir(m.ownerDir(owner))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			id := strings.TrimSuffix(e.Name(), ".json")
			if validComponent(id) {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	unlock := make([]func(), 0, len(ids))
	for _, id := range ids {
		unlock = append(unlock, m.lockSession(id))
	}
	defer func() {
		for i := len(unlock) - 1; i >= 0; i-- {
			unlock[i]()
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		m.releaseReservationLocked(id)
	}
	return os.RemoveAll(filepath.Clean(m.ownerDir(owner)))
}

type ResourceFor func(users.User) *filesvc.Resource
type CommitFunc func(context.Context, SessionView, io.Reader) error

type Manager struct {
	root              string
	resourceFor       ResourceFor
	quota             *quota.Manager
	userCount         func() int
	mu                sync.Mutex
	sessionLocks      map[string]*sessionGate
	reservations      map[string]*quota.Reservation
	reservationErrors map[string]error
}

type sessionGate struct {
	mu   sync.Mutex
	refs int
}

func New(root string, resourceFor ResourceFor, q *quota.Manager, userCount func() int) *Manager {
	_ = os.MkdirAll(root, 0o700)
	m := &Manager{root: root, resourceFor: resourceFor, quota: q, userCount: userCount, reservations: make(map[string]*quota.Reservation), reservationErrors: make(map[string]error), sessionLocks: make(map[string]*sessionGate)}
	m.reconcileAll()
	m.restoreReservations()
	return m
}

func (m *Manager) lockSession(id string) func() {
	m.mu.Lock()
	gate := m.sessionLocks[id]
	if gate == nil {
		gate = &sessionGate{}
		m.sessionLocks[id] = gate
	}
	gate.refs++
	m.mu.Unlock()
	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		m.mu.Lock()
		gate.refs--
		if gate.refs == 0 && m.sessionLocks[id] == gate {
			delete(m.sessionLocks, id)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) Create(owner users.User, path string, size int64, expectedHash string) (SessionView, error) {
	path, err := library.CleanPath(path)
	if err != nil {
		return SessionView{}, err
	}
	if size < 0 || size > int64(^uint64(0)>>1)/2 {
		return SessionView{}, errors.New("upload size is invalid")
	}
	if expectedHash != "" {
		if len(expectedHash) != sha256.Size*2 {
			return SessionView{}, errors.New("upload hash is invalid")
		}
		if _, err := hex.DecodeString(expectedHash); err != nil {
			return SessionView{}, errors.New("upload hash is invalid")
		}
	}
	id, err := newSessionID()
	if err != nil {
		return SessionView{}, err
	}
	now := time.Now().UTC()
	s := session{ID: id, OwnerID: owner.ID, Path: path, Size: size, Expected: expectedHash, Status: "uploading", CreatedAt: now, UpdatedAt: now}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.quota != nil {
		reservation, reserveErr := m.reserveBytes(owner.ID, size*2)
		if reserveErr != nil {
			return SessionView{}, reserveErr
		}
		m.reservations[id] = reservation
	}
	if err := os.MkdirAll(m.ownerDir(owner.ID), 0o700); err != nil {
		m.releaseReservationLocked(id)
		return SessionView{}, err
	}
	part, err := os.OpenFile(m.partPath(owner.ID, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		m.releaseReservationLocked(id)
		return SessionView{}, err
	}
	if err := part.Close(); err != nil {
		m.releaseReservationLocked(id)
		return SessionView{}, err
	}
	if err := m.writeLocked(s); err != nil {
		_ = os.Remove(m.partPath(owner.ID, id))
		m.releaseReservationLocked(id)
		return SessionView{}, err
	}
	return view(s), nil
}

func (m *Manager) Status(owner users.User, id string) (SessionView, error) {
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	return view(s), nil
}

func (m *Manager) List(owner users.User) ([]SessionView, error) {
	entries, err := os.ReadDir(m.ownerDir(owner.ID))
	if errors.Is(err, os.ErrNotExist) {
		return []SessionView{}, nil
	}
	if err != nil {
		return nil, err
	}
	views := make([]SessionView, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		s, err := m.Status(owner, id)
		if errors.Is(err, ErrExpired) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		views = append(views, s)
	}
	return views, nil
}

func (m *Manager) Cancel(owner users.User, id string) error {
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return err
	}
	m.releaseReservationLocked(id)
	return m.removeLocked(s)
}

func view(s session) SessionView {
	return SessionView{ID: s.ID, Path: s.Path, Size: s.Size, Offset: s.Offset, Hash: s.Hash, ChunkHashes: append([]string(nil), s.ChunkHashes...), Status: s.Status, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
