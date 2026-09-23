package library

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type trashCleanupIntent struct {
	Version          int                 `json:"version"`
	Before           time.Time           `json:"before"`
	Snapshots        []string            `json:"snapshots"`
	SharedReferences []catalog.Reference `json:"shared_references,omitempty"`
	CatalogPurged    bool                `json:"catalog_purged"`
}

func (l *Library) trashIntentPath() string {
	return filepath.Join(filepath.Dir(l.repo), "trash-cleanup.enc")
}

func (l *Library) saveTrashIntent(intent trashCleanupIntent) error {
	plain, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	sealed, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(l.trashIntentPath(), sealed, 0o600)
}

func (l *Library) loadTrashIntent() (trashCleanupIntent, error) {
	var intent trashCleanupIntent
	sealed, err := os.ReadFile(l.trashIntentPath())
	if errors.Is(err, os.ErrNotExist) {
		return intent, nil
	}
	if err != nil {
		return intent, err
	}
	plain, err := l.vault.Unwrap(sealed)
	if err != nil {
		return intent, err
	}
	defer cryptox.Zero(plain)
	if err := json.Unmarshal(plain, &intent); err != nil || intent.Version != 1 {
		return trashCleanupIntent{}, errors.New("trash cleanup intent is invalid")
	}
	for _, id := range intent.Snapshots {
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 32 {
			return trashCleanupIntent{}, errors.New("trash cleanup intent is invalid")
		}
	}
	return intent, nil
}

func (l *Library) resumeTrashCleanup(ctx context.Context) error {
	intent, err := l.loadTrashIntent()
	if err != nil {
		return err
	}
	if intent.Version == 0 {
		return nil
	}
	if !intent.CatalogPurged {
		if _, err := l.catalog.PurgeTrash(intent.Before); err != nil {
			return err
		}
		intent.CatalogPurged = true
		if err := l.saveTrashIntent(intent); err != nil {
			return err
		}
	}
	for _, ref := range intent.SharedReferences {
		if err := l.releaseReference(ctx, &ref); err != nil {
			return err
		}
	}
	if len(intent.Snapshots) > 0 {
		existing, err := l.backend.Snapshots(ctx)
		if err != nil {
			return err
		}
		wanted := make(map[string]struct{}, len(intent.Snapshots))
		for _, id := range intent.Snapshots {
			wanted[id] = struct{}{}
		}
		remaining := make([]string, 0, len(wanted))
		for _, id := range existing {
			if _, ok := wanted[id]; ok {
				remaining = append(remaining, id)
			}
		}
		deferred, err := l.backend.Forget(ctx, remaining)
		if err != nil {
			return err
		}
		if len(deferred) > 0 {
			intent.Snapshots = deferred
			intent.CatalogPurged = true
			return l.saveTrashIntent(intent)
		}
	}
	if err := os.Remove(l.trashIntentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
