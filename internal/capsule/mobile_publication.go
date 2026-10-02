package capsule

import (
	"encoding/json"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

// MobilePublicationGuard serializes only the final metadata commit with native
// credential invalidation. Callbacks must not call Store or vault methods.
type MobilePublicationGuard func(func() error) error

// GuardMobilePublication applies only to the scoped Store supplied to MobileMint.
// Ordinary browser minting cannot acquire or inherit this native authorization.
func (s *Store) GuardMobilePublication(guard MobilePublicationGuard) error {
	if s.mobileOrigin == nil || guard == nil {
		return ErrStorage
	}
	s.mobilePublish = guard
	return nil
}

func writeMobileMeta(dir string, rec Record, guard MobilePublicationGuard) error {
	if guard == nil {
		return writeMeta(dir, rec)
	}
	// Serialization and all payload/key work happen before taking the users lock.
	plain, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	plain = append(plain, '\n')
	return guard(func() error { return cryptox.AtomicWrite(filepath.Join(dir, "meta.json"), plain, 0600) })
}
