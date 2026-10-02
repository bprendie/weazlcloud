package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/bprendie/weazlcloud/internal/quota"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (m *Manager) appendEncrypted(ctx context.Context, owner users.User, id string, offset, length int64, hash string, body io.Reader) (SessionView, error) {
	if length > MaxChunkBytes {
		return SessionView{}, ErrChunkTooLarge
	}
	if len(hash) != 64 {
		return SessionView{}, errors.New("upload chunk hash is required")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return SessionView{}, errors.New("upload chunk hash is invalid")
	}
	unlock := m.lockSession(id)
	defer unlock()
	m.mu.Lock()
	s, err := m.loadLocked(owner.ID, id)
	if err == nil && offset != s.Offset {
		err = &OffsetError{Expected: s.Offset}
	}
	if err == nil {
		err = m.ensureReservationLocked(s)
	}
	if err == nil && m.reservations[id] != nil {
		bytes, e := encryptedReservation(s.Size, len(s.Parts)+1)
		err = e
		if err == nil {
			err = m.reservations[id].Resize(bytes)
		}
	}
	m.mu.Unlock()
	if err != nil {
		return SessionView{}, err
	}
	if s.Offset == s.Size || s.Status == "complete" {
		return view(s), ErrIncomplete
	}
	if length >= 0 && length > s.Size-s.Offset {
		return view(s), ErrIncomplete
	}
	key, err := segmentKey(s, len(s.Parts))
	if err != nil {
		return view(s), err
	}
	defer cryptox.Zero(key)
	chunk, err := os.OpenFile(m.chunkPath(owner.ID, id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return view(s), err
	}
	writer, err := cryptox.NewStreamFileWriter(chunk, key)
	if err != nil {
		chunk.Close()
		return view(s), err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(writer, h), io.LimitReader(&encryptedContextReader{ctx, body}, MaxChunkBytes+1))
	if copyErr == nil && n > MaxChunkBytes {
		copyErr = ErrChunkTooLarge
	}
	if copyErr == nil && (n == 0 || n > s.Size-s.Offset || length >= 0 && n != length) {
		copyErr = ErrIncomplete
	}
	if copyErr == nil && hex.EncodeToString(h.Sum(nil)) != hash {
		copyErr = ErrHashMismatch
	}
	closeErr := writer.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil {
		copyErr = chunk.Sync()
	}
	closeErr = chunk.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(m.chunkPath(owner.ID, id))
		return view(s), copyErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.PendingHash = hash
	s.PendingSize = n
	if err := m.writeLocked(s); err != nil {
		return view(s), err
	}
	if _, err := m.reconcileEncrypted(&s); err != nil {
		return view(s), err
	}
	if err := m.writeLocked(s); err != nil {
		return view(s), err
	}
	// Keep the full conservative source/commit reservation and per-PATCH
	// overhead. Status/unlock restoration reconstructs this same total.
	return view(s), nil
}

type encryptedContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *encryptedContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func encryptedReservation(size int64, parts int) (int64, error) {
	base, err := quota.SharedWriteReservation(size, 0)
	if err != nil {
		return 0, err
	}
	const perPart int64 = 8 << 10 // allocation, frames and private manifest growth
	if parts < 0 || int64(parts) > (int64(^uint64(0)>>1)-base)/perPart {
		return 0, quota.ErrExceeded
	}
	return base + int64(parts)*perPart, nil
}
