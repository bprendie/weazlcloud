package capsule

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func wrapKey(dir string, key, phrase []byte) error {
	salt, err := cryptox.Random(cryptox.SaltBytes)
	if err != nil {
		return err
	}
	pk := cryptox.Derive(phrase, salt)
	defer cryptox.Zero(pk)
	nonce, ct, err := cryptox.Seal(pk, key)
	if err != nil {
		return err
	}
	blob := append(append(append([]byte{}, salt...), nonce...), ct...)
	return cryptox.AtomicWrite(filepath.Join(dir, "pass.wrap"), blob, 0o600)
}

func (s *Store) Meta(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := readMeta(filepath.Join(s.root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrGone
		}
		return Record{}, err
	}
	if !rec.Live() {
		return rec, ErrGone
	}
	return rec, nil
}

func (s *Store) Grab(id, phrase string) ([]byte, Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grabLocked(id, phrase)
}

func (s *Store) grabLocked(id, phrase string) ([]byte, Record, error) {
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Record{}, ErrGone
		}
		return nil, Record{}, err
	}
	if !rec.Live() {
		return nil, rec, ErrGone
	}
	key, err := loadKey(dir, rec.Gate, phrase)
	if err != nil {
		return nil, rec, err
	}
	defer cryptox.Zero(key)
	blob, err := os.ReadFile(filepath.Join(dir, "payload"))
	if err != nil {
		return nil, rec, ErrGone
	}
	if len(blob) < 4 {
		return nil, rec, ErrGone
	}
	var plain []byte
	if string(blob[:len(streamMagic)]) == streamMagic {
		var buf bytes.Buffer
		if err := decryptStream(bytes.NewReader(blob[len(streamMagic):]), key, &buf); err != nil {
			return nil, rec, err
		}
		plain = buf.Bytes()
	} else {
		if len(blob) < 12 {
			return nil, rec, ErrGone
		}
		plain, err = cryptox.Open(key, blob[:12], blob[12:])
		if err != nil {
			return nil, rec, ErrGone
		}
	}
	rec.Used++
	terminal := rec.Used >= rec.Limit
	if terminal {
		rec.Revoked = true
	}
	if err := writeMeta(dir, rec); err != nil {
		cryptox.Zero(plain)
		return nil, rec, ErrStorage
	}
	if terminal {
		for _, name := range []string{"open.key", "pass.wrap", "payload"} {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				cryptox.Zero(plain)
				return nil, rec, ErrStorage
			}
		}
	}
	return plain, rec, nil
}

func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err != nil {
		return err
	}
	return s.revokeDir(dir, rec)
}

func (s *Store) revokeDir(dir string, rec Record) error {
	rec.Revoked = true
	for _, name := range []string{"open.key", "pass.wrap", "payload"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return writeMeta(dir, rec)
}

func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	out := []Record{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		rec, err := readMeta(filepath.Join(s.root, e.Name()))
		if err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func (s *Store) CleanupExpired(now time.Time) (int, error) {
	cleaned, _, err := s.CleanupExpiredBytes(now)
	return cleaned, err
}

func (s *Store) CleanupExpiredBytes(now time.Time) (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	cleaned := 0
	var reclaimed int64
	for _, entry := range ents {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(s.root, entry.Name())
		rec, err := readMeta(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return cleaned, reclaimed, err
		}
		if rec.Revoked || now.Before(rec.Expires) {
			continue
		}
		bytes, err := capsulePayloadBytes(dir)
		if err != nil {
			return cleaned, reclaimed, err
		}
		if err := s.revokeDir(dir, rec); err != nil {
			after, statErr := capsulePayloadBytes(dir)
			if statErr == nil && bytes > after {
				reclaimed += bytes - after
			}
			return cleaned, reclaimed, err
		}
		reclaimed += bytes
		cleaned++
	}
	return cleaned, reclaimed, nil
}

func capsulePayloadBytes(dir string) (int64, error) {
	var total int64
	for _, name := range []string{"open.key", "pass.wrap", "payload"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return total, err
		}
		total += info.Size()
	}
	return total, nil
}

func loadKey(dir, gate, phrase string) ([]byte, error) {
	if gate == "passphrase" {
		if phrase == "" {
			return nil, ErrPhrase
		}
		b, err := os.ReadFile(filepath.Join(dir, "pass.wrap"))
		if err != nil {
			return nil, ErrGone
		}
		if len(b) < cryptox.SaltBytes+12 {
			return nil, ErrGone
		}
		salt, rest := b[:cryptox.SaltBytes], b[cryptox.SaltBytes:]
		pk := cryptox.Derive([]byte(phrase), salt)
		defer cryptox.Zero(pk)
		key, err := cryptox.Open(pk, rest[:12], rest[12:])
		if err != nil {
			return nil, ErrPhrase
		}
		return key, nil
	}
	key, err := os.ReadFile(filepath.Join(dir, "open.key"))
	if err != nil {
		return nil, ErrGone
	}
	return key, nil
}
