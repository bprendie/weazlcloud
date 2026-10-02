package library

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"io"
	"os"
	"path/filepath"
)

// A small owner-encrypted intent binds a retry to the same immutable storage
// identity before PrepareWithID can commit any reference. It contains no bytes.
type photoComponentIntent struct {
	Version   int          `json:"version"`
	Operation string       `json:"operation"`
	File      catalog.File `json:"file"`
}

func (l *Library) photoComponentIntentPath(name string, size int64, hash string) (string, error) {
	b, _ := json.Marshal([]any{name, size, hash})
	key, err := l.vault.Fingerprint("photo-component", b)
	if err != nil {
		return "", err
	}
	defer clear(key)
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-components", hex.EncodeToString(key[:16])+".enc"), nil
}
func (l *Library) loadPhotoComponentIntent(path, name string, size int64, hash string) (photoComponentIntent, error) {
	var intent photoComponentIntent
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		id, revision, e := l.catalog.NextIdentity(name)
		if e != nil {
			return intent, e
		}
		op, e := cryptox.Random(16)
		if e != nil {
			return intent, e
		}
		intent = photoComponentIntent{Version: 1, Operation: hex.EncodeToString(op), File: catalog.File{EntryID: id, Revision: revision, Path: name, Size: size, Hash: hash}}
		return intent, l.savePhotoComponentIntent(path, intent)
	}
	if err != nil {
		return intent, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return intent, err
	}
	if len(raw) > 16<<10 {
		return intent, catalog.ErrConflict
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return intent, err
	}
	defer clear(plain)
	if json.Unmarshal(plain, &intent) != nil || intent.Version != 1 || intent.File.Path != name || intent.File.Size != size || intent.File.Hash != hash || intent.File.EntryID == "" || intent.File.Revision == 0 {
		return intent, catalog.ErrCollectionSchema
	}
	return intent, nil
}
func (l *Library) savePhotoComponentIntent(path string, intent photoComponentIntent) error {
	plain, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	defer clear(plain)
	raw, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(path, raw, 0600); err != nil {
		return err
	}
	return syncPhotoComponentDirectory(path)
}
func syncPhotoComponentDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
