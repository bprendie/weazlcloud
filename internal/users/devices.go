package users

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

// Device credentials grant Photos API access only. They never contain a vault
// key or password; locked vaults continue to reject reads and ingestion.
type Device struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id"`
	Name            string    `json:"name"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	TokenHash       string    `json:"token_hash,omitempty"`
	PasswordVersion string    `json:"password_version,omitempty"`
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hexToken(sum[:])
}

func (s *Store) CreateDevice(owner, name string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return Device{}, "", errors.New("device name must contain 1-120 bytes")
	}
	raw, err := cryptox.Random(32)
	if err != nil {
		return Device{}, "", err
	}
	token := hexToken(raw)
	cryptox.Zero(raw)
	idBytes, err := cryptox.Random(16)
	if err != nil {
		return Device{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(owner)
	if index < 0 || s.users[index].Disabled || s.users[index].Deleting {
		return Device{}, "", ErrNoSession
	}
	count := 0
	for _, device := range s.devices {
		if device.OwnerID == owner {
			count++
		}
	}
	if count >= 32 {
		return Device{}, "", errors.New("revoke an old device before adding another")
	}
	now := time.Now().UTC()
	device := Device{ID: hexToken(idBytes), OwnerID: owner, Name: name, CreatedAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour), TokenHash: tokenHash(token), PasswordVersion: tokenHash(s.users[index].Verifier)}
	s.devices = append(s.devices, device)
	if err := s.saveLocked(); err != nil {
		s.devices = s.devices[:len(s.devices)-1]
		return Device{}, "", err
	}
	return publicDevice(device), token, nil
}

func (s *Store) Devices(owner string) []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0)
	for _, device := range s.devices {
		if device.OwnerID == owner {
			out = append(out, publicDevice(device))
		}
	}
	return out
}

func publicDevice(device Device) Device {
	device.TokenHash, device.PasswordVersion = "", ""
	return device
}

func (s *Store) RevokeDevice(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, device := range s.devices {
		if device.ID != id || device.OwnerID != owner {
			continue
		}
		previous := s.devices
		next := append([]Device(nil), previous[:i]...)
		s.devices = append(next, previous[i+1:]...)
		if err := s.saveLocked(); err != nil {
			s.devices = previous
			return err
		}
		return nil
	}
	return ErrNoSession
}

func deviceRoute(path string) bool {
	return path == "/api/v1/photos" || strings.HasPrefix(path, "/api/v1/photos/") || path == "/api/v1/devices" || path == "/api/v1/devices/revoke"
}

func (s *Store) DeviceForRequest(r *http.Request) (Device, error) {
	if !deviceRoute(r.URL.Path) {
		return Device{}, ErrNoSession
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) != 64 {
		return Device{}, ErrNoSession
	}
	hash := tokenHash(parts[1])
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, device := range s.devices {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(device.TokenHash)) != 1 || !time.Now().Before(device.ExpiresAt) {
			continue
		}
		index := s.indexLocked(device.OwnerID)
		if index < 0 || s.users[index].Disabled || s.users[index].Deleting || device.PasswordVersion != tokenHash(s.users[index].Verifier) {
			return Device{}, ErrNoSession
		}
		return publicDevice(device), nil
	}
	return Device{}, ErrNoSession
}

func (s *Store) deviceUser(r *http.Request) (User, error) {
	device, err := s.DeviceForRequest(r)
	if err != nil {
		return User{}, err
	}
	u, ok := s.User(device.OwnerID)
	if !ok || u.Disabled || u.Deleting {
		return User{}, ErrNoSession
	}
	return u, nil
}

func (s *Store) removeOwnerDevicesLocked(owner string) []Device {
	previous := s.devices
	next := make([]Device, 0, len(previous))
	for _, device := range previous {
		if device.OwnerID != owner {
			next = append(next, device)
		}
	}
	s.devices = next
	return previous
}
