package library

import (
	"context"
	"io"
	"path"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type PhotoExport struct {
	ID, Name, MediaType string
	Revision            uint64
	Size                int64
	Original            func(io.Writer) error
	Preview             func() ([]byte, string, error)
}

// PreparePhotoExport pins immutable original references before minting a
// public grant. Call Release after the encrypted capsule is durably committed.
func (l *Library) PreparePhotoExport(ctx context.Context, ids []string, hidden bool) ([]PhotoExport, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.preparePhotoExportLocked(ctx, ids, hidden)
}

func (l *Library) preparePhotoExportLocked(ctx context.Context, ids []string, hidden bool) ([]PhotoExport, func(), error) {
	if !l.vault.Unlocked() {
		return nil, nil, vault.ErrLocked
	}
	if len(ids) == 0 || len(ids) > 10_000 {
		return nil, nil, ErrPhotoSearch
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return nil, nil, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	holds := &archiveHolds{}
	release := func() { ArchiveManifest{holds: holds}.Release() }
	fail := func(err error) ([]PhotoExport, func(), error) { release(); return nil, nil, err }
	out := make([]PhotoExport, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	allIDs := append([]string(nil), ids...)
	for _, id := range ids {
		if file, ok := l.photoByID[id]; ok {
			for _, part := range file.PhotoComponents {
				component, present := l.photoByID[part.AssetID]
				if part.ID != "original" && present && l.photoPathHiddenLocked(component.Path) == hidden {
					allIDs = append(allIDs, part.AssetID)
				}
			}
		}
	}
	if len(allIDs) > 10000 {
		return fail(ErrPhotoSearch)
	}
	for _, id := range allIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		file, ok := l.photoByID[id]
		if !ok || file.Folder || !photoMedia(file.Path) || l.photoPathHiddenLocked(file.Path) != hidden {
			return fail(catalog.ErrNotFound)
		}
		ref, err := l.capture(file)
		if err != nil {
			return fail(err)
		}
		done, err := l.holdReference(ref)
		if err != nil {
			return fail(err)
		}
		holds.releases = append(holds.releases, done)
		out = append(out, PhotoExport{ID: id, Name: path.Base(file.Path), MediaType: photoMediaType(file.Path), Revision: file.Revision, Size: file.Size,
			Original: func(dst io.Writer) error { return l.readReference(ctx, ref, dst) },
			Preview: func() ([]byte, string, error) {
				// The renderer reads the captured revision and re-encodes pixels;
				// embedded EXIF/GPS is never copied into this derivative.
				return l.thumbnailFor(ctx, file, 1280, false)
			},
		})
	}
	return out, release, nil
}
