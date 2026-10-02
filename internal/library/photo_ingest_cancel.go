package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// CancelPhotoIngest serializes cancellation with publication. A matching logical
// pair wins even after a move or trash operation. Otherwise tombstone must persist
// before private rows/references are discarded; a retry safely repeats cleanup.
func (l *Library) CancelPhotoIngest(ctx context.Context, commit catalog.PhotoIngestCommit, tombstone func() error) (catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if commit.DeviceID == "" || commit.DeviceAssetID == "" || commit.SourceRevision == "" || len(commit.Files) < 1 || len(commit.Files) > 2 || tombstone == nil {
		return catalog.File{}, catalog.ErrConflict
	}
	for _, p := range commit.Files {
		if !strings.HasPrefix(p.From, ".weazl-mobile-pending/") || path.Clean(p.From) != p.From {
			return catalog.File{}, catalog.ErrConflict
		}
	}
	if !l.vault.Unlocked() {
		return catalog.File{}, vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	files := l.catalog.All()
	byID := make(map[string]catalog.File, len(files))
	for _, f := range files {
		byID[f.EntryID] = f
	}
	for _, f := range files {
		if f.DeviceID != commit.DeviceID || f.DeviceAssetID != commit.DeviceAssetID || f.SourceRevision != commit.SourceRevision {
			continue
		}
		if f.PhotoParentID != "" {
			continue
		}
		if len(f.PhotoComponents) != len(commit.Files) {
			return catalog.File{}, catalog.ErrConflict
		}
		for i, p := range commit.Files {
			component := f.PhotoComponents[i]
			part, ok := byID[component.AssetID]
			if !ok || component.ID != p.ID || part.Hash != p.Hash || part.Size != p.Size || part.DeviceID != commit.DeviceID || part.DeviceAssetID != commit.DeviceAssetID || part.SourceRevision != commit.SourceRevision {
				return catalog.File{}, catalog.ErrConflict
			}
		}
		return f, nil
	}
	if err := tombstone(); err != nil {
		return catalog.File{}, err
	}
	// Keep the private rows until their shared reference release has succeeded:
	// crashes can then retry release without losing its immutable binding.
	for _, p := range commit.Files {
		f, ok := l.catalog.Get(p.From)
		if ok {
			if f.Folder || f.DeviceID != "" || f.PhotoParentID != "" || len(f.PhotoComponents) != 0 || f.Size != p.Size || f.Hash != p.Hash {
				return catalog.File{}, catalog.ErrConflict
			}
			if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
				if err := l.sharedStore.Release(ctx, l.ownerID, f.Reference.OwnerEntryID, f.Reference.OwnerRevision); err != nil {
					return catalog.File{}, err
				}
			}
		}
		intentPath, err := l.photoComponentIntentPath(p.From, p.Size, p.Hash)
		if err != nil {
			return catalog.File{}, err
		}
		if _, err = os.Stat(intentPath); err == nil {
			intent, e := l.loadPhotoComponentIntent(intentPath, p.From, p.Size, p.Hash)
			if e != nil {
				return catalog.File{}, e
			}
			if l.sharedStore != nil {
				if e = l.sharedStore.Recover(ctx, intent.Operation, false); e != nil && !errors.Is(e, sharedstore.ErrState) && !errors.Is(e, sql.ErrNoRows) {
					return catalog.File{}, e
				}
				if e = l.sharedStore.Release(ctx, l.ownerID, intent.File.EntryID, intent.File.Revision); e != nil {
					return catalog.File{}, e
				}
			}
			if e = os.Remove(intentPath); e != nil && !errors.Is(e, os.ErrNotExist) {
				return catalog.File{}, e
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return catalog.File{}, err
		}
	}
	return catalog.File{}, l.catalog.DiscardPhotoPending(commit.Files)
}
