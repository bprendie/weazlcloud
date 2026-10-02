package users

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func equalHash(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func bearerToken(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validDeviceSecret(parts[1]) {
		return ""
	}
	return parts[1]
}
func (s *Store) credentialLocked(token string) (int, bool) {
	hash := tokenHash(token)
	now := s.nowLocked()
	for i, d := range s.devices {
		if !s.deviceActiveLocked(d) {
			continue
		}
		if equalHash(hash, d.TokenHash) {
			return i, false
		}
		if equalHash(hash, d.PreviousHash) && now.Before(d.PreviousUntil) && now.Before(d.PreviousExpires) {
			return i, true
		}
	}
	return -1, false
}
func (s *Store) DeviceForRequest(r *http.Request) (Device, error) {
	token := bearerToken(r)
	if token == "" {
		return Device{}, ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, _ := s.credentialLocked(token)
	if i < 0 {
		return Device{}, ErrNoSession
	}
	d := publicDevice(s.devices[i])
	if !d.AllowsRequest(r) {
		return Device{}, ErrInsufficientScope
	}
	return d, nil
}
func (s *Store) deviceUser(r *http.Request) (User, error) {
	d, err := s.DeviceForRequest(r)
	if err != nil {
		return User{}, err
	}
	u, ok := s.User(d.OwnerID)
	if !ok || u.Disabled || u.Deleting {
		return User{}, ErrNoSession
	}
	return u, nil
}
