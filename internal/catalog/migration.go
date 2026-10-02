package catalog

import (
	"bytes"
	"errors"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"os"
	"path/filepath"
)

var ErrMigrationRecoveryConflict = errors.New("catalog pre-upgrade recovery copy differs; restore a consistent snapshot")

// RecoveryPath contains the exact encrypted pre-schema-2 catalog, not plaintext.
func (c *Catalog) RecoveryPath() string { return c.path + ".pre-schema-2.enc" }
func (c *Catalog) preserveMigrationRecovery(raw []byte) error {
	previous, err := os.ReadFile(c.RecoveryPath())
	if err == nil {
		if !bytes.Equal(previous, raw) {
			return ErrMigrationRecoveryConflict
		}
		return syncCatalogDirectory(c.RecoveryPath())
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := cryptox.AtomicWrite(c.RecoveryPath(), raw, 0600); err != nil {
		return err
	}
	// AtomicWrite fsyncs contents before rename; sync the directory before replacing
	// the original catalog so power loss cannot persist only the migrated catalog.
	return syncCatalogDirectory(c.RecoveryPath())
}
func syncCatalogDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
