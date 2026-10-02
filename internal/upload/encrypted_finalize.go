package upload

import (
	"context"
	"io"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/users"
)

// encryptedPayloadReader opens only one authenticated segment at a time.
// Its memory bound is the cryptox frame size, independent of total input size.
type encryptedPayloadReader struct {
	manager *Manager
	session session
	ctx     context.Context
	index   int
	current *cryptox.StreamFileReader
}

func (r *encryptedPayloadReader) Read(p []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.current == nil {
			if r.index == len(r.session.Parts) {
				return 0, io.EOF
			}
			key, err := segmentKey(r.session, r.index)
			if err != nil {
				return 0, err
			}
			r.current, err = cryptox.OpenStreamFile(r.ctx, r.manager.segmentPath(r.session, r.index), key, r.session.Parts[r.index])
			cryptox.Zero(key)
			if err != nil {
				return 0, err
			}
		}
		n, err := r.current.Read(p)
		if err == io.EOF {
			_ = r.current.Close()
			r.current = nil
			r.index++
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func (r *encryptedPayloadReader) Close() error {
	if r.current != nil {
		err := r.current.Close()
		r.current = nil
		return err
	}
	return nil
}
func (m *Manager) finalizeEncrypted(ctx context.Context, owner users.User, id string, commit CommitFunc) (SessionView, error) {
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	s, err := m.loadLocked(owner.ID, id)
	if err == nil && s.Status == "complete" {
		m.mu.Unlock()
		return view(s), nil
	}
	if err == nil {
		err = m.ensureReservationLocked(s)
	}
	if err == nil && s.Offset != s.Size {
		err = ErrIncomplete
	}
	if err == nil {
		s.Status = "finalizing"
		err = m.writeLocked(s)
	}
	m.mu.Unlock()
	if err != nil {
		return view(s), err
	}
	body := &encryptedPayloadReader{manager: m, session: s, ctx: ctx}
	hash, err := hashReader(body)
	_ = body.Close()
	if err == nil && s.Expected != "" && hash != s.Expected {
		err = ErrHashMismatch
	}
	s.Hash = hash
	if err == nil {
		m.mu.Lock()
		err = m.writeLocked(s)
		m.mu.Unlock()
	}
	if err == nil {
		body = &encryptedPayloadReader{manager: m, session: s, ctx: ctx}
		err = commit(ctx, view(s), body)
		closeErr := body.Close()
		if err == nil {
			err = closeErr
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		s.Status = "ready"
		_ = m.writeLocked(s)
		return view(s), err
	}
	s.Status = "complete"
	if err := m.writeLocked(s); err != nil {
		return view(s), err
	}
	if err := m.removePayloadLocked(s); err != nil {
		return view(s), err
	}
	m.releaseReservationLocked(id)
	return view(s), nil
}
