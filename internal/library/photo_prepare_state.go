package library

import (
	"encoding/json"
	"errors"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
	"io"
	"os"
	"path/filepath"
)

func (l *Library) photoPreparationPath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-preparation.enc")
}

func (l *Library) loadPhotoPreparationLocked() {
	if l.photoPrepLoaded {
		return
	}
	l.photoPrepLoaded = true
	f, err := os.Open(l.photoPreparationPath())
	if err != nil {
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, photoPreparationStateLimit+1))
	if err != nil || len(raw) > photoPreparationStateLimit {
		return
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return
	}
	defer cryptox.Zero(plain)
	var state photoPreparation
	if json.Unmarshal(plain, &state) != nil || state.Total < 0 || state.Position < 0 || state.Ready < 0 || state.Failed < 0 || state.Position > 2_000_000 || state.Total > 2_000_000 {
		return
	}
	l.photoPrep = state
	// Old positional checkpoints are unsafe after rebuilding/sorting an index.
	l.photoPrep.Position, l.photoPrep.Ready, l.photoPrep.Failed = 0, 0, 0
	l.photoPrep.Paused = state.Paused || state.Status == "paused"
	if state.Enabled && !l.photoPrep.Paused {
		l.photoPrep.Status = "queued"
	}
}

func (l *Library) savePhotoPreparationLocked() (err error) {
	defer func() {
		if err != nil && !errors.Is(err, vault.ErrLocked) {
			l.photoPrep.Status = "paused_error"
			l.photoPrep.Error = err.Error()
		}
	}()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	plain, err := json.Marshal(l.photoPrep)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err == nil {
		err = cryptox.AtomicWrite(l.photoPreparationPath(), wrapped, 0o600)
	}
	return err
}
