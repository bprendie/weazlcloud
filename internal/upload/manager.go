package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

const MaxChunkBytes int64 = 16 << 20

type SessionView struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Offset    int64     `json:"offset"`
	Hash      string    `json:"hash,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ResourceFor func(users.User) *filesvc.Resource
type CommitFunc func(context.Context, SessionView, io.Reader) error

type Manager struct {
	root        string
	resourceFor ResourceFor
	quota       *quota.Manager
	userCount   func() int
	mu          sync.Mutex
}

func New(root string, resourceFor ResourceFor, q *quota.Manager, userCount func() int) *Manager {
	_ = os.MkdirAll(root, 0o700)
	m := &Manager{root: root, resourceFor: resourceFor, quota: q, userCount: userCount}
	m.reconcileAll()
	return m
}

func (m *Manager) Create(owner users.User, path string, size int64, expectedHash string) (SessionView, error) {
	path, err := library.CleanPath(path)
	if err != nil {
		return SessionView{}, err
	}
	if size < 0 {
		return SessionView{}, errors.New("upload size must not be negative")
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
	if err := os.MkdirAll(m.ownerDir(owner.ID), 0o700); err != nil {
		return SessionView{}, err
	}
	part, err := os.OpenFile(m.partPath(owner.ID, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return SessionView{}, err
	}
	if err := part.Close(); err != nil {
		return SessionView{}, err
	}
	if err := m.writeLocked(s); err != nil {
		_ = os.Remove(m.partPath(owner.ID, id))
		return SessionView{}, err
	}
	return view(s), nil
}

func (m *Manager) Status(owner users.User, id string) (SessionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	if s.Status == "complete" {
		return view(s), nil
	}
	return view(s), nil
}

func (m *Manager) Append(ctx context.Context, owner users.User, id string, offset, length int64, body io.Reader) (SessionView, error) {
	if length > MaxChunkBytes {
		return SessionView{}, ErrChunkTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	if offset != s.Offset {
		return SessionView{}, &OffsetError{Expected: s.Offset}
	}
	if s.Status == "ready" || s.Status == "finalizing" || s.Offset == s.Size {
		return SessionView{}, errors.New("upload is already complete")
	}
	remaining := s.Size - s.Offset
	if length >= 0 && length > remaining {
		return SessionView{}, errors.New("upload chunk exceeds expected size")
	}
	chunk, err := os.OpenFile(m.chunkPath(owner.ID, id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return SessionView{}, err
	}
	limit := io.LimitReader(body, MaxChunkBytes+1)
	n, copyErr := io.Copy(chunk, limit)
	if copyErr == nil && n > MaxChunkBytes {
		copyErr = ErrChunkTooLarge
	}
	if copyErr == nil && length >= 0 && n != length {
		copyErr = fmt.Errorf("upload chunk length changed: got %d, want %d", n, length)
	}
	if copyErr == nil && n > remaining {
		copyErr = errors.New("upload chunk exceeds expected size")
	}
	if copyErr == nil && n == 0 {
		copyErr = errors.New("upload chunk is empty")
	}
	if copyErr == nil {
		copyErr = chunk.Sync()
	}
	closeErr := chunk.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(m.chunkPath(owner.ID, id))
		return SessionView{}, copyErr
	}
	chunk, err = os.Open(m.chunkPath(owner.ID, id))
	if err != nil {
		return SessionView{}, err
	}
	err = m.finishChunkLocked(s, chunk, n)
	closeErr = chunk.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return SessionView{}, err
	}
	if err := os.Remove(m.chunkPath(owner.ID, id)); err != nil {
		return SessionView{}, err
	}
	s.Offset += n
	s.Status = "uploading"
	if s.Offset == s.Size {
		s.Status = "ready"
	}
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	return view(s), nil
}

func (m *Manager) Finalize(ctx context.Context, owner users.User, id string, commit CommitFunc) (SessionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	if s.Status == "complete" {
		return view(s), nil
	}
	if s.Offset != s.Size {
		return view(s), ErrIncomplete
	}
	s.Status = "finalizing"
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	hash, err := hashFile(m.partPath(owner.ID, id))
	if err != nil {
		s.Status = "ready"
		_ = m.writeLocked(s)
		return SessionView{}, err
	}
	s.Hash = hash
	if s.Expected != "" && s.Expected != hash {
		s.Status = "ready"
		_ = m.writeLocked(s)
		return view(s), ErrHashMismatch
	}
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	body, err := os.Open(m.partPath(owner.ID, id))
	if err != nil {
		s.Status = "ready"
		_ = m.writeLocked(s)
		return SessionView{}, err
	}
	v := view(s)
	commitErr := commit(ctx, v, body)
	closeErr := body.Close()
	if commitErr == nil {
		commitErr = closeErr
	}
	if commitErr != nil {
		s.Status = "ready"
		_ = m.writeLocked(s)
		return v, commitErr
	}
	s.Status = "complete"
	if err := m.writeLocked(s); err != nil {
		return v, err
	}
	if err := m.removePayloadLocked(s); err != nil {
		return v, err
	}
	return view(s), nil
}

func (m *Manager) Cancel(owner users.User, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return err
	}
	return m.removeLocked(s)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func view(s session) SessionView {
	return SessionView{ID: s.ID, Path: s.Path, Size: s.Size, Offset: s.Offset, Hash: s.Hash, Status: s.Status, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

func (m *Manager) Reserve(owner users.User, bytes, current int64, res *filesvc.Resource) (func(), error) {
	if m.quota == nil {
		return func() {}, nil
	}
	used, err := res.Lib.Usage(context.Background())
	if err != nil {
		return nil, err
	}
	count := 1
	if m.userCount != nil {
		count = m.userCount()
	}
	return m.quota.Reserve(owner.ID, count, used, current, bytes)
}

func (m *Manager) Resource(owner users.User) *filesvc.Resource {
	if m.resourceFor == nil {
		return nil
	}
	return m.resourceFor(owner)
}
