package upload

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const encryptedManifestMagic = "WZU2\n"

func (m *Manager) ownerVault(owner string) (*vault.Vault, error) {
	if m.resourceFor == nil {
		return nil, errors.New("upload owner vault resolver is unavailable")
	}
	res := m.Resource(users.User{ID: owner})
	if res == nil || res.Vault == nil {
		return nil, ErrNotFound
	}
	return res.Vault, nil
}
func (m *Manager) decodeManifest(owner string, raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte(encryptedManifestMagic)) {
		return raw, nil
	}
	v, err := m.ownerVault(owner)
	if err != nil {
		return nil, err
	}
	return v.Unwrap(raw[len(encryptedManifestMagic):])
}
func (m *Manager) encodeManifest(s session, plain []byte) ([]byte, error) {
	if s.Format != 2 {
		return plain, nil
	}
	v, err := m.ownerVault(s.OwnerID)
	if err != nil {
		return nil, err
	}
	raw, err := v.Wrap(plain)
	if err != nil {
		return nil, err
	}
	return append([]byte(encryptedManifestMagic), raw...), nil
}
func (m *Manager) isEncrypted(owner, id string) (bool, error) {
	if !validComponent(owner) || !validComponent(id) {
		return false, ErrNotFound
	}
	file, err := os.Open(m.manifestPath(owner, id))
	if errors.Is(err, os.ErrNotExist) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	var prefix [5]byte
	n, err := file.Read(prefix[:])
	if err != nil {
		return false, err
	}
	return n == 5 && string(prefix[:]) == encryptedManifestMagic, nil
}
func (m *Manager) createPayload(s *session) error {
	// Resolver-less managers remain a legacy-only drain/test adapter. Every
	// production owner registry supplies a vault, so all its new sessions are v2.
	if m.resourceFor != nil {
		v, err := m.ownerVault(s.OwnerID)
		if err != nil {
			return err
		}
		if !v.Unlocked() {
			return vault.ErrLocked
		}
		key, err := cryptox.Random(32)
		if err != nil {
			return err
		}
		defer cryptox.Zero(key)
		s.Format = 2
		s.Key = cryptox.B64(key)
		return os.Mkdir(m.partPath(s.OwnerID, s.ID), 0700)
	}
	file, err := os.OpenFile(m.partPath(s.OwnerID, s.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return file.Close()
}
func (m *Manager) segmentPath(s session, index int) string {
	return filepath.Join(m.partPath(s.OwnerID, s.ID), fmt.Sprintf("%08d.enc", index))
}
func segmentKey(s session, index int) ([]byte, error) {
	key, err := cryptox.B64d(s.Key)
	if err != nil || len(key) != 32 {
		return nil, ErrCorrupt
	}
	defer cryptox.Zero(key)
	h := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(h, "weazl-upload:%s:%d", s.ID, index)
	return h.Sum(nil), nil
}

func syncUploadDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
