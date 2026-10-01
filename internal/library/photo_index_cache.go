package library

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const photoIndexCacheLimit = 128 << 20

type photoIndexSnapshot struct {
	Version uint8          `json:"version"`
	Epoch   uint64         `json:"epoch"`
	Rows    []catalog.File `json:"rows"`
}

func (l *Library) photoIndexCachePath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photos-index.enc")
}

func (l *Library) loadPhotoIndexCache() ([]catalog.File, uint64, bool) {
	file, err := os.Open(l.photoIndexCachePath())
	if err != nil {
		return nil, 0, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, photoIndexCacheLimit+1))
	if err != nil || len(raw) > photoIndexCacheLimit {
		return nil, 0, false
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return nil, 0, false
	}
	defer clear(plain)
	var snapshot photoIndexSnapshot
	if json.Unmarshal(plain, &snapshot) != nil || snapshot.Version != 1 || snapshot.Epoch == 0 || len(snapshot.Rows) > 2_000_000 {
		return nil, 0, false
	}
	for _, f := range snapshot.Rows {
		if !f.Present || !inPhotoRoot(f.Path) {
			return nil, 0, false
		}
	}
	return snapshot.Rows, snapshot.Epoch, true
}

func (l *Library) schedulePhotoIndexSaveLocked() {
	l.photoSaveEpoch = l.photoEpoch
	if l.photoSave != nil && l.photoSave.Stop() {
		l.photoSaveWG.Done()
	}
	l.photoSaveWG.Add(1)
	l.photoSave = time.AfterFunc(1500*time.Millisecond, func() {
		defer l.photoSaveWG.Done()
		l.persistPhotoIndex()
	})
}

// The timer coalesces Takeout's per-file change events into one encrypted
// snapshot write. A failed cache write is harmless; catalog state is canonical.
func (l *Library) persistPhotoIndex() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return
	}
	l.photoMu.Lock()
	if !l.photoReady || l.photoSaveEpoch != l.photoEpoch {
		l.photoMu.Unlock()
		return
	}
	rows := append([]catalog.File(nil), l.photoRows...)
	sortPhotoRows(rows)
	snapshot := photoIndexSnapshot{Version: 1, Epoch: l.photoEpoch, Rows: rows}
	l.photoMu.Unlock()
	plain, err := json.Marshal(snapshot)
	if err != nil || len(plain) > photoIndexCacheLimit-1024 {
		clear(plain)
		return
	}
	defer clear(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return
	}
	_ = cryptox.AtomicWrite(l.photoIndexCachePath(), wrapped, 0o600)
}
