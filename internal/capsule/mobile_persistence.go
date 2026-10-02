package capsule

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func readMobileOperation(v *vault.Vault, name string) (mobileOperation, error) {
	var op mobileOperation
	raw, err := os.ReadFile(name)
	if err != nil {
		return op, err
	}
	plain, err := v.Unwrap(raw)
	if err != nil {
		return op, err
	}
	defer clear(plain)
	err = json.Unmarshal(plain, &op)
	return op, err
}

func writeMobileOperation(v *vault.Vault, name string, op mobileOperation) error {
	plain, err := json.Marshal(op)
	if err != nil {
		return err
	}
	defer clear(plain)
	raw, err := v.Wrap(plain)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(name, raw, 0600); err != nil {
		return err
	}
	if err := syncMobileDir(filepath.Dir(name)); err != nil {
		return err
	}
	return syncMobileDir(filepath.Dir(filepath.Dir(name)))
}

func syncMobileDir(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
