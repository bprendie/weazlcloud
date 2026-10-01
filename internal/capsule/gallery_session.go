package capsule

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type GalleryAuth struct{ Passphrase, Session string }
type gallerySession struct {
	ID      string
	Expires time.Time
	Key     []byte
}

// Guest tokens contain only one capsule key, sealed with the node's private
// signing material. Every use still checks the capsule's live/revoked state.
func (s *Store) GallerySession(id, phrase string) (GalleryManifest, string, error) {
	return s.RenewGallerySession(id, GalleryAuth{Passphrase: phrase})
}

func (s *Store) RenewGallerySession(id string, auth GalleryAuth) (GalleryManifest, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, manifest, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return manifest, "", err
	}
	defer clear(key)
	expires := time.Now().Add(15 * time.Minute)
	if rec.Expires.Before(expires) {
		expires = rec.Expires
	}
	plain, err := json.Marshal(gallerySession{ID: id, Expires: expires, Key: key})
	if err != nil {
		return manifest, "", err
	}
	defer clear(plain)
	secret, err := s.gallerySessionSecretLocked()
	if err != nil {
		return manifest, "", err
	}
	nonce, sealed, err := cryptox.Seal(secret, plain)
	if err != nil {
		return manifest, "", err
	}
	return manifest, base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (s *Store) gallerySessionSecretLocked() ([]byte, error) {
	if len(s.gallerySecret) != 0 {
		return s.gallerySecret, nil
	}
	name := filepath.Join(s.root, "gallery-session.key")
	key, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		key, err = cryptox.Random(cryptox.KeyBytes)
		if err == nil {
			err = cryptox.AtomicWrite(name, key, 0600)
		}
	}
	if err != nil || len(key) != cryptox.KeyBytes {
		clear(key)
		return nil, ErrStorage
	}
	s.gallerySecret = key
	return key, nil
}

func (s *Store) galleryAuthKeyLocked(dir string, rec Record, auth GalleryAuth) ([]byte, error) {
	if auth.Session == "" {
		return loadKey(dir, rec.Gate, auth.Passphrase)
	}
	if len(auth.Session) > 2048 {
		return nil, ErrPhrase
	}
	raw, err := base64.RawURLEncoding.DecodeString(auth.Session)
	if err != nil || len(raw) < 12 {
		return nil, ErrPhrase
	}
	secret, err := s.gallerySessionSecretLocked()
	if err != nil {
		return nil, err
	}
	plain, err := cryptox.Open(secret, raw[:12], raw[12:])
	if err != nil {
		return nil, ErrPhrase
	}
	defer clear(plain)
	var session gallerySession
	if json.Unmarshal(plain, &session) != nil || session.ID != rec.ID || !time.Now().Before(session.Expires) || len(session.Key) != cryptox.KeyBytes {
		clear(session.Key)
		return nil, ErrPhrase
	}
	return session.Key, nil
}
