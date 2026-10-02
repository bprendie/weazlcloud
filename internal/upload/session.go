package upload

import (
	"bytes"
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
	Format          int       `json:"format,omitempty"`
	Key             string    `json:"key,omitempty"`
	Parts           []int64   `json:"parts,omitempty"`
	PendingSize     int64     `json:"pending_size,omitempty"`
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id"`
	Path            string    `json:"path"`
	Size            int64     `json:"size"`
	Offset          int64     `json:"offset"`
	Expected        string    `json:"expected_hash,omitempty"`
	IdempotencyHash string    `json:"idempotency_hash,omitempty"`
	Hash            string    `json:"hash,omitempty"`
	ChunkHashes     []string  `json:"chunk_hashes,omitempty"`
	PendingHash     string    `json:"pending_hash,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
	s, err := m.readSessionLocked(owner, id)
	if err != nil {
		return session{}, err
	}
	if s.Status != "complete" && time.Since(s.UpdatedAt) >= SessionLifetime {
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

func (m *Manager) readSessionLocked(owner, id string) (session, error) {
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
	sealed := bytes.HasPrefix(b, []byte(encryptedManifestMagic))
	b, err = m.decodeManifest(owner, b)
	if err != nil {
		return session{}, err
	}
	defer cryptox.Zero(b)
	var s session
	if err := json.Unmarshal(b, &s); err != nil {
		return session{}, ErrCorrupt
	}
	if sealed != (s.Format == 2) || s.Format != 0 && s.Format != 2 || s.ID != id || s.OwnerID != owner || s.Size < 0 || s.Offset < 0 || s.Offset > s.Size || len(s.IdempotencyHash) > 64 {
		return session{}, ErrCorrupt
	}
	return s, nil
}

func (m *Manager) writeLocked(s session) error {
	s.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	defer cryptox.Zero(b)
	b, err = m.encodeManifest(s, b)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(m.manifestPath(s.OwnerID, s.ID), append(b, '\n'), 0o600); err != nil {
		return err
	}
	if s.Format == 2 {
		return syncUploadDir(m.ownerDir(s.OwnerID))
	}
	return nil
}

func (m *Manager) removeLocked(s session) error {
	var first error
	for _, path := range []string{m.manifestPath(s.OwnerID, s.ID), m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) removePayloadLocked(s session) error {
	var first error
	for _, path := range []string{m.partPath(s.OwnerID, s.ID), m.chunkPath(s.OwnerID, s.ID)} {
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) reconcileLocked(s *session) (bool, error) {
	if s.Format == 2 {
		return m.reconcileEncrypted(s)
	}
	return m.reconcileLegacy(s)
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
