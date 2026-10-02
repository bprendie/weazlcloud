package users

import (
	"crypto/sha256"
	"errors"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"strings"
	"time"
)

// Device is a durable identity; secret fields are stripped from public views.
// nil Scopes means legacy photos:v1; an explicit empty slice grants no resources.
type Device struct {
	ID                   string    `json:"id"`
	OwnerID              string    `json:"owner_id"`
	Name                 string    `json:"name"`
	CreatedAt            time.Time `json:"created_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	Scopes               []string  `json:"scopes"`
	Generation           uint64    `json:"generation"`
	AuthorizationVersion uint64    `json:"authorization_version"`
	Revoked              bool      `json:"revoked"`
	TokenHash            string    `json:"token_hash,omitempty"`
	PasswordVersion      string    `json:"password_version,omitempty"`
	PreviousHash         string    `json:"previous_hash,omitempty"`
	PreviousUntil        time.Time `json:"previous_until,omitempty"`
	PreviousExpires      time.Time `json:"previous_expires,omitempty"`
	RotationID           string    `json:"rotation_id,omitempty"`
	RotationExpected     uint64    `json:"rotation_expected,omitempty"`
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hexToken(sum[:])
}

// SetClock injects the device credential clock. nil restores wall time.
func (s *Store) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
}
func (s *Store) nowLocked() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) CreateDevice(owner, name string) (Device, string, error) {
	return s.CreateScopedDevice(owner, name, nil)
}

func (s *Store) CreateScopedDevice(owner, name string, scopes []string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return Device{}, "", errors.New("device name must contain 1-120 bytes")
	}
	grants, err := normalizeScopes(scopes)
	if err != nil {
		return Device{}, "", err
	}
	raw, err := cryptox.Random(32)
	if err != nil {
		return Device{}, "", err
	}
	token := hexToken(raw)
	cryptox.Zero(raw)
	id, err := cryptox.Random(16)
	if err != nil {
		return Device{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(owner)
	if index < 0 || s.users[index].Disabled || s.users[index].Deleting {
		return Device{}, "", ErrNoSession
	}
	if s.activeDeviceCountLocked(owner) >= 32 {
		return Device{}, "", ErrDeviceLimit
	}
	now := s.nowLocked()
	device := Device{ID: hexToken(id), OwnerID: owner, Name: name, CreatedAt: now, ExpiresAt: now.Add(deviceTTL), Scopes: grants, Generation: 1, AuthorizationVersion: 1, TokenHash: tokenHash(token), PasswordVersion: tokenHash(s.users[index].Verifier)}
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
	for _, d := range s.devices {
		if d.OwnerID == owner {
			out = append(out, publicDevice(d))
		}
	}
	return out
}
func publicDevice(d Device) Device {
	d.TokenHash, d.PasswordVersion, d.PreviousHash, d.RotationID = "", "", "", ""
	d.PreviousUntil, d.PreviousExpires = time.Time{}, time.Time{}
	d.RotationExpected = 0
	if d.Scopes != nil {
		d.Scopes = append([]string{}, d.Scopes...)
	}
	if d.Generation == 0 {
		d.Generation = 1
	}
	if d.AuthorizationVersion == 0 {
		d.AuthorizationVersion = 1
	}
	return d
}
func (s *Store) RevokeDevice(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devices {
		if d.ID == id && d.OwnerID == owner {
			next := d
			invalidateDevice(&next)
			return s.replaceDeviceLocked(i, next)
		}
	}
	return ErrNoSession
}
func invalidateDevice(d *Device) {
	d.Revoked = true
	d.TokenHash = ""
	d.PreviousHash = ""
	d.RotationID = ""
	d.AuthorizationVersion = publicDevice(*d).AuthorizationVersion + 1
}
func (s *Store) invalidateOwnerDevicesLocked(owner string) []Device {
	old := s.devices
	s.devices = append([]Device(nil), old...)
	for i := range s.devices {
		if s.devices[i].OwnerID == owner {
			invalidateDevice(&s.devices[i])
		}
	}
	return old
}
func (s *Store) removeOwnerDevicesLocked(owner string) []Device {
	old := s.devices
	next := make([]Device, 0, len(old))
	for _, d := range old {
		if d.OwnerID != owner {
			next = append(next, d)
		}
	}
	s.devices = next
	return old
}
func (s *Store) activeDeviceCountLocked(owner string) int {
	count := 0
	for _, d := range s.devices {
		if d.OwnerID == owner && s.deviceActiveLocked(d) {
			count++
		}
	}
	return count
}
func (s *Store) deviceActiveLocked(d Device) bool {
	i := s.indexLocked(d.OwnerID)
	return i >= 0 && !s.users[i].Disabled && !s.users[i].Deleting && !d.Revoked && d.TokenHash != "" && s.nowLocked().Before(d.ExpiresAt) && d.PasswordVersion == tokenHash(s.users[i].Verifier)
}
func (s *Store) replaceDeviceLocked(i int, d Device) error {
	old := s.devices[i]
	s.devices[i] = d
	if err := s.saveLocked(); err != nil {
		s.devices[i] = old
		return err
	}
	return nil
}
