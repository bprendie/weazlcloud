package upload

import (
	"context"
	"errors"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (m *Manager) reconcileEncrypted(s *session) (bool, error) {
	if s.Format != 2 || s.PendingSize < 0 || len(s.Parts) != len(s.ChunkHashes) {
		return false, ErrCorrupt
	}
	if s.Status == "complete" {
		return false, m.removePayloadLocked(*s)
	}
	info, err := os.Stat(m.partPath(s.OwnerID, s.ID))
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, ErrCorrupt
	}
	changed := false
	if s.PendingSize > 0 {
		if s.PendingSize > MaxChunkBytes || s.PendingSize > s.Size-s.Offset || len(s.PendingHash) != 64 {
			return false, ErrCorrupt
		}
		final := m.segmentPath(*s, len(s.Parts))
		candidate := final
		if _, err := os.Stat(final); errors.Is(err, os.ErrNotExist) {
			candidate = m.chunkPath(s.OwnerID, s.ID)
		} else if err != nil {
			return false, err
		}
		key, err := segmentKey(*s, len(s.Parts))
		if err != nil {
			return false, err
		}
		reader, err := cryptox.OpenStreamFile(context.Background(), candidate, key, s.PendingSize)
		cryptox.Zero(key)
		if err != nil {
			return false, err
		}
		hash, err := hashReader(reader)
		_ = reader.Close()
		if err != nil {
			return false, err
		}
		if hash != s.PendingHash {
			return false, ErrCorrupt
		}
		if candidate != final {
			if err := os.Rename(candidate, final); err != nil {
				return false, err
			}
		}
		if err := syncUploadDir(m.partPath(s.OwnerID, s.ID)); err != nil {
			return false, err
		}
		if err := syncUploadDir(m.ownerDir(s.OwnerID)); err != nil {
			return false, err
		}
		s.Parts = append(s.Parts, s.PendingSize)
		s.ChunkHashes = append(s.ChunkHashes, s.PendingHash)
		s.Offset += s.PendingSize
		s.PendingHash = ""
		s.PendingSize = 0
		changed = true
	}
	// An incomplete unaccepted encrypted chunk has no publication marker. Only
	// this transient file may be discarded; accepted segment files are retained.
	if err := os.Remove(m.chunkPath(s.OwnerID, s.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	total := int64(0)
	for i, size := range s.Parts {
		if size <= 0 || size > MaxChunkBytes || total > s.Size-size {
			return false, ErrCorrupt
		}
		key, err := segmentKey(*s, i)
		if err != nil {
			return false, err
		}
		reader, err := cryptox.OpenStreamFile(context.Background(), m.segmentPath(*s, i), key, size)
		cryptox.Zero(key)
		if err != nil {
			return false, err
		}
		_ = reader.Close()
		total += size
	}
	if total != s.Offset {
		return false, ErrCorrupt
	}
	status := "uploading"
	if s.Offset == s.Size {
		status = "ready"
	}
	if s.Status != status {
		s.Status = status
		changed = true
	}
	return changed, nil
}
