package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

type Commit func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error)

func (m *Manager) Process(ctx context.Context, res *filesvc.Resource, id string, authorize func(Session) error, commit Commit) error {
	key := keyFor(res, id)
	u := m.lock(res, id)
	s, e := m.load(res, id)
	if e != nil {
		u()
		return e
	}
	m.mu.Lock()
	if m.active[key] != nil || m.hasReceiversLocked(key) {
		m.mu.Unlock()
		u()
		return nil
	}
	if m.draining[s.OwnerID] > 0 {
		m.mu.Unlock()
		u()
		return ErrConflict
	}
	ctx, cancel := context.WithCancel(ctx)
	job := &activeJob{owner: s.OwnerID, cancel: cancel, done: make(chan struct{})}
	m.active[key] = job
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.active, key)
		close(job.done)
		m.mu.Unlock()
	}()
	if (s.Status != "queued" && s.Status != "verifying") || m.now().Before(s.RetryAfter) {
		u()
		return ErrConflict
	}
	if e = authorize(s); e != nil {
		u()
		return e
	}
	s.Status = "verifying"
	e = m.save(res, &s)
	u()
	if e != nil {
		return e
	}
	e = m.Verify(ctx, res, id)
	var result json.RawMessage
	if e == nil {
		e = authorize(s)
	}
	if e == nil {
		result, e = commit(s, func(component string) (io.ReadCloser, error) {
			if e := authorize(s); e != nil {
				return nil, e
			}
			return m.Open(ctx, res, id, component)
		})
	}
	u = m.lock(res, id)
	defer u()
	latest, loadErr := m.load(res, id)
	if loadErr != nil {
		return loadErr
	}
	if e != nil {
		latest.Status = "failed"
		latest.ErrorCode = failureCode(e)
		if errors.Is(e, ErrChecksum) {
			latest.ErrorCode = "checksum_mismatch"
		}
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			latest.Status = "queued"
			latest.ErrorCode = ""
		} else {
			// A batch storage failure need not mean this member is corrupt.
			// Reopen its durable encrypted parts, with bounded persisted backoff.
			var retryable interface{ Retryable() bool }
			if errors.As(e, &retryable) && retryable.Retryable() && latest.RetryAttempts < 3 {
				latest.RetryAttempts++
				latest.Status, latest.ErrorCode = "queued", "storage_retry"
				latest.RetryAfter = m.now().Add(time.Second * time.Duration(1<<latest.RetryAttempts))
			}
		}
		if saveErr := m.save(res, &latest); saveErr != nil {
			return saveErr
		}
		return e
	}
	latest.Status = "stored"
	latest.RetryAfter = time.Time{}
	latest.Result = result
	latest.ErrorCode = ""
	if e = m.save(res, &latest); e != nil {
		return e
	}
	e = m.removePayload(res, id)
	m.release(res, id)
	return e
}
func (m *Manager) removePayload(res *filesvc.Resource, id string) error {
	entries, e := os.ReadDir(keyFor(res, id))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wza") || strings.HasSuffix(entry.Name(), ".receipt.enc") || strings.HasPrefix(entry.Name(), ".incoming-") {
			if e = os.Remove(filepath.Join(keyFor(res, id), entry.Name())); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
	}
	return syncDir(keyFor(res, id))
}
