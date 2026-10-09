package mobileparts

import "github.com/bprendie/weazlcloud/internal/cryptox"

// validateSession must precede recovery, cleanup, expiry, or quota mutations.
// Unknown versions and states are preserved for a compatible reader.
func validateSession(s Session, id string) error {
	if s.Version != 1 || !validID(id) || s.ID != id || !validID(s.OwnerID) || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return ErrCorrupt
	}
	if s.RetryAttempts < 0 || s.RetryAttempts > 3 {
		return ErrCorrupt
	}
	spec := s.Spec
	spec.Components = append([]Component(nil), spec.Components...)
	if spec.normalize() != nil {
		return ErrCorrupt
	}
	for _, c := range s.Spec.Components {
		if c.ReceivedParts < 0 || c.ReceivedParts > count(c) || c.ReceivedBytes < 0 || c.ReceivedBytes > c.Size {
			return ErrCorrupt
		}
	}
	key, err := cryptox.B64d(s.Key)
	defer clear(key)
	if err != nil || len(key) != 32 {
		return ErrCorrupt
	}
	switch s.Status {
	case "uploading", "queued", "verifying", "failed", "stored", "cancelled":
		return nil
	default:
		return ErrCorrupt
	}
}
