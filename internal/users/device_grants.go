package users

import (
	"net/http"
	"time"
)

// DeviceGrant is safe to persist in an owner-encrypted async manifest. Rotation
// preserves it; reauthorization, revoke, password change and disable invalidate it.
// ExpiresAt is the admitted generation's expiry, never silently extended by jobs.
type DeviceGrant struct {
	OwnerID              string    `json:"owner_id"`
	DeviceID             string    `json:"device_id"`
	AuthorizationVersion uint64    `json:"authorization_version"`
	ExpiresAt            time.Time `json:"expires_at"`
	Scopes               []string  `json:"scopes"`
}

func (s *Store) GrantForRequest(r *http.Request, scopes ...string) (DeviceGrant, error) {
	token := bearerToken(r)
	if token == "" {
		return DeviceGrant{}, ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, previous := s.credentialLocked(token)
	if i < 0 {
		return DeviceGrant{}, ErrNoSession
	}
	d := publicDevice(s.devices[i])
	if !d.AllowsRequest(r) || !deviceScopes(d, scopes) {
		return DeviceGrant{}, ErrInsufficientScope
	}
	expiry := d.ExpiresAt
	if previous {
		expiry = s.devices[i].PreviousExpires
	}
	return DeviceGrant{OwnerID: d.OwnerID, DeviceID: d.ID, AuthorizationVersion: d.AuthorizationVersion, ExpiresAt: expiry, Scopes: append([]string{}, scopes...)}, nil
}
func deviceScopes(d Device, scopes []string) bool {
	if d.Scopes == nil {
		for _, v := range scopes {
			if v != PhotosRead && v != PhotosWrite && v != LegacyPhotos {
				return false
			}
		}
		return true
	}
	return d.HasScopes(scopes...)
}
func (s *Store) checkGrantLocked(g DeviceGrant, scopes []string) error {
	if !s.nowLocked().Before(g.ExpiresAt) {
		return ErrNoSession
	}
	for _, d := range s.devices {
		if d.ID == g.DeviceID && d.OwnerID == g.OwnerID {
			if !s.deviceActiveLocked(d) || publicDevice(d).AuthorizationVersion != g.AuthorizationVersion {
				return ErrNoSession
			}
			for _, want := range scopes {
				found := false
				for _, got := range g.Scopes {
					if want == got {
						found = true
					}
				}
				if !found {
					return ErrInsufficientScope
				}
			}
			if !deviceScopes(d, g.Scopes) {
				return ErrInsufficientScope
			}
			return nil
		}
	}
	return ErrNoSession
}
func (s *Store) CheckDeviceGrant(g DeviceGrant, scopes ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkGrantLocked(g, scopes)
}

// WithDeviceGrant serializes the final publish with device/account invalidation.
// publish MUST NOT call authorization mutation methods. Ordinary grant reads
// use only the short state mutex; persistence never holds it. Lock order is
// publication barrier then state mutex. Caller still holds its vault/lifecycle
// lease and checks destination revisions atomically in its own catalog commit.
func (s *Store) WithDeviceGrant(g DeviceGrant, publish func() error, scopes ...string) error {
	s.publishMu.RLock()
	defer s.publishMu.RUnlock()
	s.mu.Lock()
	err := s.checkGrantLocked(g, scopes)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return publish()
}

// CheckDeviceOperation hides foreign owner/device IDs and rejects a mismatched
// coordinator kind. Cookie management stays in the parent handler explicitly.
func (s *Store) CheckDeviceOperation(r *http.Request, owner, device, kind string) (Device, error) {
	d, err := s.DeviceForRequest(r)
	if err != nil {
		return Device{}, err
	}
	if d.OwnerID != owner || d.ID != device {
		return Device{}, ErrNoSession
	}
	var required []string
	switch kind {
	case "photos":
		required = []string{PhotosWrite}
	case "backup":
		required = []string{BackupWrite, FilesWrite}
	case "files":
		required = []string{FilesWrite}
	default:
		return Device{}, ErrInsufficientScope
	}
	if !deviceScopes(d, required) {
		return Device{}, ErrInsufficientScope
	}
	return d, nil
}

// ActiveDevice checks current credential and account state without a raw token.
// It preserves the stable ID across rotations. Persist DeviceGrant when a job
// must also distinguish authorization epochs after owner reauthorization.
func (s *Store) ActiveDevice(owner, id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.OwnerID == owner && d.ID == id && s.deviceActiveLocked(d) {
			return publicDevice(d), true
		}
	}
	return Device{}, false
}

// HasScope maps legacy photos:v1 to Photos read/write only, never generic grants.
func HasScope(device Device, scope string) bool {
	return deviceScopes(device, []string{scope})
}
