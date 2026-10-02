package users

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const deviceTTL = 90 * 24 * time.Hour
const DeviceGrace = 15 * time.Minute

var (
	ErrRotationConflict = errors.New("credential generation or operation conflict")
	ErrInvalidRotation  = errors.New("invalid credential rotation")
	ErrDeviceLimit      = errors.New("revoke an old device before adding another")
)

func validDeviceSecret(secret string) bool {
	if len(secret) != 64 {
		return false
	}
	_, err := hex.DecodeString(secret)
	return err == nil
}
func validRotation(operation, secret string, expected uint64) bool {
	return len(operation) > 0 && len(operation) <= 128 && strings.TrimSpace(operation) == operation && !strings.ContainsAny(operation, "\x00\r\n") && validDeviceSecret(secret) && expected > 0
}

// RotateDevice uses the client-prepared 32-byte hex secret; it never returns it.
// A previous credential may replay only the last identical operation during grace.
func (s *Store) RotateDevice(r *http.Request, id, operation string, expected uint64, replacement string) (Device, error) {
	if !validRotation(operation, replacement, expected) {
		return Device{}, ErrInvalidRotation
	}
	token := bearerToken(r)
	if token == "" {
		return Device{}, ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, previous := s.credentialLocked(token)
	if i < 0 || s.devices[i].ID != id {
		return Device{}, ErrNoSession
	}
	d := s.devices[i]
	hash := tokenHash(replacement)
	if d.RotationID == tokenHash(operation) {
		if d.RotationExpected == expected && equalHash(d.TokenHash, hash) {
			return publicDevice(d), nil
		}
		return Device{}, ErrRotationConflict
	}
	generation := publicDevice(d).Generation
	if previous || expected != generation || generation == ^uint64(0) || equalHash(hash, d.TokenHash) || equalHash(hash, d.PreviousHash) {
		return Device{}, ErrRotationConflict
	}
	// A secret cannot alias another device's current or grace credential.
	for j, other := range s.devices {
		if j != i && (equalHash(hash, other.TokenHash) || equalHash(hash, other.PreviousHash)) {
			return Device{}, ErrRotationConflict
		}
	}
	d.PreviousHash, d.PreviousExpires = d.TokenHash, d.ExpiresAt
	d.PreviousUntil = s.nowLocked().Add(DeviceGrace)
	if d.PreviousExpires.Before(d.PreviousUntil) {
		d.PreviousUntil = d.PreviousExpires
	}
	d.TokenHash = hash
	d.Generation = generation + 1
	d.ExpiresAt = s.nowLocked().Add(deviceTTL)
	d.RotationID, d.RotationExpected = tokenHash(operation), expected
	if err := s.replaceDeviceLocked(i, d); err != nil {
		return Device{}, err
	}
	return publicDevice(d), nil
}

// ReauthorizeDevice requires an owner cookie session and preserves the identity.
// Omitted scopes preserve existing grants, including an explicit empty grant set.
func (s *Store) ReauthorizeDevice(r *http.Request, id, replacement string, scopes []string) (Device, error) {
	if r.Header.Get("Authorization") != "" {
		return Device{}, ErrNoSession
	}
	u, err := s.Current(r)
	if err != nil {
		return Device{}, err
	}
	if !validDeviceSecret(replacement) {
		return Device{}, ErrInvalidRotation
	}
	grants, err := normalizeScopes(scopes)
	if err != nil {
		return Device{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := s.indexLocked(u.ID)
	// Recheck session under the mutation lock, so password/disable cannot race it.
	cookie, cookieErr := r.Cookie(cookieName)
	if cookieErr != nil {
		return Device{}, ErrNoSession
	}
	sess, ok := s.sessions[cookie.Value]
	if !ok || sess.userID != u.ID || !time.Now().Before(sess.expires) || owner < 0 || s.users[owner].Disabled || s.users[owner].Deleting {
		return Device{}, ErrNoSession
	}
	for i, d := range s.devices {
		if d.ID == id && d.OwnerID == u.ID {
			if !s.deviceActiveLocked(d) && s.activeDeviceCountLocked(u.ID) >= 32 {
				return Device{}, ErrDeviceLimit
			}
			hash := tokenHash(replacement)
			for _, other := range s.devices {
				if equalHash(hash, other.TokenHash) || equalHash(hash, other.PreviousHash) {
					return Device{}, ErrRotationConflict
				}
			}
			view := publicDevice(d)
			if view.Generation == ^uint64(0) {
				return Device{}, ErrRotationConflict
			}
			d.Generation = view.Generation + 1
			d.AuthorizationVersion = view.AuthorizationVersion + 1
			d.TokenHash = hash
			d.PasswordVersion = tokenHash(s.users[owner].Verifier)
			d.ExpiresAt = s.nowLocked().Add(deviceTTL)
			d.Revoked = false
			d.PreviousHash, d.RotationID = "", ""
			d.PreviousUntil, d.PreviousExpires = time.Time{}, time.Time{}
			d.RotationExpected = 0
			if scopes != nil {
				d.Scopes = grants
			}
			if err := s.replaceDeviceLocked(i, d); err != nil {
				return Device{}, err
			}
			return publicDevice(d), nil
		}
	}
	return Device{}, ErrNoSession
}
