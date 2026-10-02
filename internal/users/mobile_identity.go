package users

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"golang.org/x/sys/unix"
)

// InstanceID identifies this data volume, not a hostname or connection origin.
// A restored volume preserves it. A cloned independent node must remove only
// instance_id while stopped to generate a new ID; clients must explicitly approve
// that new identity. Home/remote aliases must match it AND use trusted TLS origins.
func (s *Store) InstanceID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(filepath.Dir(s.path), "instance_id")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return "", err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		_, decodeErr := hex.DecodeString(id)
		if len(id) != 32 || decodeErr != nil {
			return "", errors.New("invalid node instance identity")
		}
		return id, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	raw, err := cryptox.Random(16)
	if err != nil {
		return "", err
	}
	id := hexToken(raw)
	if err := cryptox.AtomicWrite(path, []byte(id+"\n"), 0600); err != nil {
		return "", err
	}
	return id, nil
}
