package library

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// A canceled publication may leave a verified reference only in its encrypted
// retry intent. Other members of that same snapshot may already be in Trash.
func (l *Library) photoComponentSnapshots() (map[string]bool, error) {
	protected := make(map[string]bool)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(l.repo), ".weazl-photo-components", "*.enc"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		if len(raw) > 16<<10 {
			return nil, catalog.ErrConflict
		}
		plain, err := l.vault.Unwrap(raw)
		if err != nil {
			return nil, err
		}
		var intent photoComponentIntent
		err = json.Unmarshal(plain, &intent)
		clear(plain)
		if err != nil {
			return nil, err
		}
		if intent.Version != 1 {
			return nil, catalog.ErrConflict
		}
		if ref := intent.File.Reference; ref != nil && ref.Backend == catalog.ResticBackend {
			protected[ref.Snapshot] = true
		}
	}
	return protected, nil
}
