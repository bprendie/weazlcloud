package upload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type session struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	Offset      int64     `json:"offset"`
	Expected    string    `json:"expected_hash,omitempty"`
	Hash        string    `json:"hash,omitempty"`
	ChunkHashes []string  `json:"chunk_hashes,omitempty"`
	PendingHash string    `json:"pending_hash,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var componentRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

func newSessionID() (string, error) {
	b, err := cryptox.Random(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) ownerDir(owner string) string { return filepath.Join(m.root, owner) }
func (m *Manager) manifestPath(owner, id string) string {
	return filepath.Join(m.ownerDir(owner), id+".json")
}
func (m *Manager) partPath(owner, id string) string {
	return filepath.Join(m.ownerDir(owner), id+".part")
}
func (m *Manager) chunkPath(owner, id string) string {
	return filepath.Join(m.ownerDir(owner), id+".chunk")
}

func validComponent(value string) bool { return componentRE.MatchString(value) }

func (m *Manager) loadLocked(owner, id string) (session, error) {
	if !validComponent(owner) || !validComponent(id) {
		return session{}, ErrNotFound
	}
	b, err := os.ReadFile(m.manifestPath(owner, id))
	if errors.Is(err, os.ErrNotExist) {
		return session{}, ErrNotFound
	}
	if err != nil {
		return session{}, err
	}
	var s session
	if err := json.Unmarshal(b, &s); err != nil {
		return session{}, ErrCorrupt
	}
	if s.ID != id || s.OwnerID != owner || s.Size < 0 || s.Offset < 0 || s.Offset > s.Size {
		return session{}, ErrCorrupt
	}
	if s.Status != "complete" && time.Since(s.UpdatedAt) >= SessionLifetime {
		if err := m.removeLocked(s); err != nil {
			return session{}, err
		}
		m.releaseReservationLocked(id)
		return session{}, ErrExpired
	}
	changed, err := m.reconcileLocked(&s)
	if err != nil {
		return session{}, err
	}
	if changed {
		if err := m.writeLocked(s); err != nil {
			return session{}, err
		}
	}
	return s, nil
}

func (m *Manager) writeLocked(s session) error {
	s.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(m.manifestPath(s.OwnerID, s.ID), append(b, '\n'), 0o600)
}

func (m *Manager) removeLocked(s session) error {
	var first error
	for _, path := range []string{m.manifestPath(s.OwnerID, s.ID), m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) removePayloadLocked(s session) error {
	var first error
	for _, path := range []string{m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) reconcileLocked(s *session) (bool, error) {
	if s.Status == "complete" {
		for _, path := range []string{m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
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

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashSegment(file *os.File, offset, length int64) (string, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	return hashReader(io.LimitReader(file, length))
}

func (m *Manager) finishChunkLocked(s session, chunk *os.File, size int64) error {
	part, err := os.OpenFile(m.partPath(s.OwnerID, s.ID), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer part.Close()
	if err := part.Truncate(s.Offset); err != nil {
		return err
	}
	if _, err := part.Seek(s.Offset, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyN(part, chunk, size); err != nil {
		return err
	}
	return part.Sync()
}
