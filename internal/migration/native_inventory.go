package migration

import (
	"encoding/json"
	"errors"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"io"
	"os"
	"path/filepath"
)

func pendingUploads(dataDir string, store *users.Store, user users.User) (int, error) {
	ordered, err := pendingOrderedUploads(dataDir, store, user)
	if err != nil {
		return ordered, err
	}
	native, err := pendingNativeUploads(store, user)
	return ordered + native, err
}
func pendingNativeUploads(store *users.Store, user users.User) (int, error) {
	root := filepath.Join(filepath.Dir(store.LibraryPath(user)), ".weazl-mobile-parts")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
	if err = v.UnlockNode(); err != nil {
		return 0, err
	}
	defer v.Lock()
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := readInventoryEnvelope(filepath.Join(root, entry.Name(), "session.enc"))
		if err != nil {
			return count, err
		}
		plain, err := v.Unwrap(raw)
		if err != nil {
			return count, err
		}
		var header struct {
			Version int    `json:"version"`
			ID      string `json:"id"`
			OwnerID string `json:"owner_id"`
			Status  string `json:"status"`
		}
		err = json.Unmarshal(plain, &header)
		clear(plain)
		if err != nil {
			return count, err
		}
		if header.Version != 1 || header.ID != entry.Name() || header.OwnerID != user.ID {
			return count, errors.New("unsupported native upload manifest or identity")
		}
		switch header.Status {
		case "stored", "cancelled":
		case "uploading", "queued", "verifying", "failed":
			count++
		default:
			return count, errors.New("unsupported native upload status")
		}
	}
	return count, nil
}
func readInventoryEnvelope(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 256<<10 {
		return nil, errors.New("native upload manifest too large")
	}
	return b, nil
}
