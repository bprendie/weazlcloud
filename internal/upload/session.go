package upload

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type session struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Offset    int64     `json:"offset"`
	Expected  string    `json:"expected_hash,omitempty"`
	Hash      string    `json:"hash,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
		defer chunk.Close()
		info, err := chunk.Stat()
		if err != nil {
			return false, err
		}
		if info.Size() == 0 || info.Size() > MaxChunkBytes || s.Offset+info.Size() > s.Size {
			return false, ErrCorrupt
		}
		if err := m.finishChunkLocked(*s, chunk, info.Size()); err != nil {
			return false, err
		}
		s.Offset += info.Size()
		changed = true
		if err := os.Remove(m.chunkPath(s.OwnerID, s.ID)); err != nil {
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
	if info.Size() > s.Offset {
		if info.Size() > s.Size {
			part.Close()
			return false, ErrCorrupt
		}
		s.Offset = info.Size()
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

func (m *Manager) reconcileAll() {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return
	}
	for _, owner := range entries {
		if !owner.IsDir() || !validComponent(owner.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(m.root, owner.Name()))
		if err != nil {
			continue
		}
		for _, entry := range files {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				id := strings.TrimSuffix(entry.Name(), ".json")
				m.mu.Lock()
				if s, loadErr := m.loadLocked(owner.Name(), id); loadErr == nil && s.Status == "ready" {
					_ = m.writeLocked(s)
				}
				m.mu.Unlock()
			}
		}
	}
}
