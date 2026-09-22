package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/users"
)

func (m *Manager) Append(ctx context.Context, owner users.User, id string, offset, length int64, chunkHash string, body io.Reader) (SessionView, error) {
	if length > MaxChunkBytes {
		return SessionView{}, ErrChunkTooLarge
	}
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	if offset != s.Offset {
		return SessionView{}, &OffsetError{Expected: s.Offset}
	}
	if reserveErr := m.reservationErrors[id]; reserveErr != nil {
		return SessionView{}, reserveErr
	}
	if len(chunkHash) != sha256.Size*2 {
		return SessionView{}, errors.New("upload chunk hash is required")
	}
	if _, err := hex.DecodeString(chunkHash); err != nil {
		return SessionView{}, errors.New("upload chunk hash is invalid")
	}
	if s.Status == "ready" || s.Status == "finalizing" || s.Offset == s.Size {
		return SessionView{}, errors.New("upload is already complete")
	}
	remaining := s.Size - s.Offset
	if length >= 0 && length > remaining {
		return SessionView{}, errors.New("upload chunk exceeds expected size")
	}
	if _, exists := m.reservations[id]; !exists && m.quota != nil {
		reservation, reserveErr := m.reserveBytes(owner.ID, (s.Size-s.Offset)+s.Size)
		if reserveErr != nil {
			return SessionView{}, reserveErr
		}
		m.reservations[id] = reservation
	}
	s.PendingHash = chunkHash
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	chunk, err := os.OpenFile(m.chunkPath(owner.ID, id), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		s.PendingHash = ""
		_ = m.writeLocked(s)
		return SessionView{}, err
	}
	m.mu.Unlock()
	locked = false
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
		chunk.Seek(0, io.SeekStart)
		got, hashErr := hashReader(chunk)
		if hashErr != nil {
			copyErr = hashErr
		} else if got != chunkHash {
			copyErr = ErrHashMismatch
		}
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
		m.mu.Lock()
		locked = true
		s.PendingHash = ""
		_ = m.writeLocked(s)
		return SessionView{}, copyErr
	}
	chunk, err = os.Open(m.chunkPath(owner.ID, id))
	if err != nil {
		return SessionView{}, err
	}
	m.mu.Lock()
	locked = true
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
	s.ChunkHashes = append(s.ChunkHashes, chunkHash)
	s.PendingHash = ""
	s.Status = "uploading"
	if s.Offset == s.Size {
		s.Status = "ready"
	}
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	if reservation := m.reservations[id]; reservation != nil {
		if err := reservation.Resize(2*s.Size - s.Offset); err != nil {
			return view(s), err
		}
	}
	return view(s), nil
}

func (m *Manager) Finalize(ctx context.Context, owner users.User, id string, commit CommitFunc) (SessionView, error) {
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	s, err := m.loadLocked(owner.ID, id)
	if err != nil {
		return SessionView{}, err
	}
	if s.Status == "complete" {
		return view(s), nil
	}
	if err := m.ensureReservationLocked(s); err != nil {
		return view(s), err
	}
	if s.Offset != s.Size {
		return view(s), ErrIncomplete
	}
	s.Status = "finalizing"
	if err := m.writeLocked(s); err != nil {
		return SessionView{}, err
	}
	m.mu.Unlock()
	locked = false
	hash, err := hashFile(m.partPath(owner.ID, id))
	m.mu.Lock()
	locked = true
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
	m.mu.Unlock()
	locked = false
	commitErr := commit(ctx, v, body)
	closeErr := body.Close()
	m.mu.Lock()
	locked = true
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
	m.releaseReservationLocked(id)
	return view(s), nil
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
