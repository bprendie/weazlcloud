package upload

import (
	"errors"
	"os"
)

func (m *Manager) reconcileLegacy(s *session) (bool, error) {
	if s.Status == "complete" {
		for _, path := range []string{m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
			if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		}
		return false, nil
	}
	changed := false
	chunk, chunkErr := os.Open(m.chunkPath(s.OwnerID, s.ID))
	if chunkErr == nil {
		info, err := chunk.Stat()
		if err != nil {
			chunk.Close()
			return false, err
		}
		if info.Size() == 0 || info.Size() > MaxChunkBytes || info.Size() > s.Size {
			chunk.Close()
			return false, ErrCorrupt
		}
		chunkHash, hashErr := hashReader(chunk)
		chunk.Close()
		if hashErr != nil {
			return false, hashErr
		}
		if s.PendingHash != "" && chunkHash == s.PendingHash {
			if info.Size() > s.Size-s.Offset {
				return false, ErrCorrupt
			}
			chunk, err = os.Open(m.chunkPath(s.OwnerID, s.ID))
			if err != nil {
				return false, err
			}
			if err := m.finishChunkLocked(*s, chunk, info.Size()); err != nil {
				chunk.Close()
				return false, err
			}
			chunk.Close()
			s.Offset += info.Size()
			s.ChunkHashes = append(s.ChunkHashes, chunkHash)
			s.PendingHash = ""
			changed = true
		} else if s.PendingHash == "" && len(s.ChunkHashes) > 0 && chunkHash == s.ChunkHashes[len(s.ChunkHashes)-1] {
			// The manifest was advanced and synced, but the process stopped
			// before deleting the already committed chunk journal.
		} else {
			_ = os.Remove(m.chunkPath(s.OwnerID, s.ID))
			if s.PendingHash != "" {
				s.PendingHash = ""
				changed = true
			}
		}
		if err := os.Remove(m.chunkPath(s.OwnerID, s.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	} else if !errors.Is(chunkErr, os.ErrNotExist) {
		return false, chunkErr
	}
	part, err := os.OpenFile(m.partPath(s.OwnerID, s.ID), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	info, statErr := part.Stat()
	if statErr != nil {
		part.Close()
		return false, statErr
	}
	if info.Size() < s.Offset {
		part.Close()
		return false, ErrCorrupt
	}
	if s.Offset > 0 && len(s.ChunkHashes) == 0 {
		for start := int64(0); start < s.Offset; start += BrowserChunkBytes {
			length := min(BrowserChunkBytes, s.Offset-start)
			hash, hashErr := hashSegment(part, start, length)
			if hashErr != nil {
				part.Close()
				return false, hashErr
			}
			s.ChunkHashes = append(s.ChunkHashes, hash)
		}
		changed = true
	}
	if info.Size() > s.Offset {
		if info.Size() > s.Size {
			part.Close()
			return false, ErrCorrupt
		}
		if s.PendingHash == "" {
			part.Close()
			return false, ErrCorrupt
		}
		hash, hashErr := hashSegment(part, s.Offset, info.Size()-s.Offset)
		if hashErr != nil || hash != s.PendingHash {
			if err := part.Truncate(s.Offset); err != nil {
				part.Close()
				return false, err
			}
			s.PendingHash = ""
		} else {
			s.ChunkHashes = append(s.ChunkHashes, hash)
			s.Offset = info.Size()
			s.PendingHash = ""
		}
		changed = true
	}
	if err := part.Close(); err != nil {
		return false, err
	}
	want := "uploading"
	if s.Offset == s.Size {
		want = "ready"
	}
	if s.Status == "finalizing" || s.Status != want {
		s.Status = want
		changed = true
	}
	return changed, nil
}
