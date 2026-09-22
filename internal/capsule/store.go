package capsule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type Store struct {
	mu   sync.Mutex
	root string
}

func New(root string) *Store { return &Store{root: root} }

func (s *Store) Mint(rec Record, phrase string, payload []byte) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := cryptox.Random(16)
	if err != nil {
		return Record{}, err
	}
	rec.ID = encodeToken(id)
	key, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return Record{}, err
	}
	dir := filepath.Join(s.root, rec.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Record{}, err
	}
	nonce, ct, err := cryptox.Seal(key, payload)
	if err != nil {
		return Record{}, err
	}
	blob := append(append([]byte{}, nonce...), ct...)
	if err := cryptox.AtomicWrite(filepath.Join(dir, "payload"), blob, 0o600); err != nil {
		return Record{}, err
	}
	if rec.Gate == "passphrase" {
		if phrase == "" {
			os.RemoveAll(dir)
			return Record{}, ErrPhrase
		}
		if err := wrapKey(dir, key, []byte(phrase)); err != nil {
			os.RemoveAll(dir)
			return Record{}, err
		}
	} else {
		rec.Gate = "open"
		if err := cryptox.AtomicWrite(filepath.Join(dir, "open.key"), key, 0o600); err != nil {
			return Record{}, err
		}
	}
	cryptox.Zero(key)
	if err := writeMeta(dir, rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (s *Store) ListOwner(owner string) []Record {
	recs := s.List()
	out := make([]Record, 0, len(recs))
	for _, rec := range recs {
		if rec.Owner == owner {
			out = append(out, rec)
		}
	}
	return out
}

func (s *Store) RevokeOwner(id, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err != nil {
		return err
	}
	if rec.Owner != owner {
		return ErrGone
	}
	return s.revokeDir(dir, rec)
}

func (s *Store) RevokeAll(owner string) error   { return s.ownerCapsules(owner, false) }
func (s *Store) DeleteOwner(owner string) error { return s.ownerCapsules(owner, true) }

func (s *Store) ownerCapsules(owner string, remove bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		rec, readErr := readMeta(dir)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if rec.Owner != owner {
			continue
		}
		if remove {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		} else if !rec.Revoked {
			if err := s.revokeDir(dir, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) AssignOwner(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		rec, err := readMeta(dir)
		if err != nil || rec.Owner != "" {
			continue
		}
		rec.Owner = owner
		if err := writeMeta(dir, rec); err != nil {
			return err
		}
	}
	return nil
}

func encodeToken(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = hex[v>>4], hex[v&0x0f]
	}
	return string(out)
}

func writeMeta(dir string, rec Record) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(filepath.Join(dir, "meta.json"), append(b, '\n'), 0o600)
}

func readMeta(dir string) (Record, error) {
	var rec Record
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(b, &rec)
	return rec, err
}
