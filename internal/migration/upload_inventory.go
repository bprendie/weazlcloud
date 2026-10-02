package migration

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"strings"
)

// Read-only adapter for ordered manifest formats: legacy JSON (0) and WZU2
// owner-vault-wrapped JSON (2). Never reconcile, expire or rewrite staging here.
func migrationUploadStatus(store *users.Store, user users.User, name string, raw []byte) (string, error) {
	const magic = "WZU2\n"
	sealed := bytes.HasPrefix(raw, []byte(magic))
	plain := raw
	if sealed {
		v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
		if err := v.UnlockNode(); err != nil {
			return "", err
		}
		defer v.Lock()
		var err error
		plain, err = v.Unwrap(raw[len(magic):])
		if err != nil {
			return "", err
		}
		defer clear(plain)
	}
	var s struct {
		Format  int    `json:"format"`
		Status  string `json:"status"`
		ID      string `json:"id"`
		OwnerID string `json:"owner_id"`
	}
	if err := json.Unmarshal(plain, &s); err != nil {
		return "", err
	}
	if sealed != (s.Format == 2) || (s.Format != 0 && s.Format != 2) {
		return "", errors.New("unsupported upload manifest format")
	}
	if sealed && (s.OwnerID != user.ID || s.ID != strings.TrimSuffix(name, ".json")) {
		return "", errors.New("upload manifest identity mismatch")
	}
	switch s.Status {
	case "uploading", "ready", "finalizing", "complete":
		return s.Status, nil
	case "":
		if !sealed {
			return "uploading", nil
		}
	}
	return "", errors.New("unsupported upload manifest status")
}
