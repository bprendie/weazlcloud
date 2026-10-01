package library

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const PhotoSelectionLifetime = 90 * time.Minute

type PhotoSelectionOptions struct {
	IDs    []string           `json:"ids,omitempty"`
	Filter PhotoSearchOptions `json:"filter"`
	Mode   string             `json:"mode,omitempty"`
	Date   string             `json:"date,omitempty"`
	Search bool               `json:"search,omitempty"`
}

type PhotoSelectionView struct {
	ID        string    `json:"id"`
	Count     int       `json:"count"`
	Hidden    bool      `json:"hidden"`
	ExpiresAt time.Time `json:"expires_at"`
}
type selectedPhotoVersion struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}
type photoSelectionManifest struct {
	PhotoSelectionView
	Versions []selectedPhotoVersion `json:"versions"`
}

func (l *Library) photoSelectionDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-selections")
}
func validSelectionID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16
}

// Only the count and opaque token cross the wire. Large day/album selections
// remain encrypted and revision-bound on the owner node.
func (l *Library) CreatePhotoSelection(ctx context.Context, options PhotoSelectionOptions) (PhotoSelectionView, error) {
	if !l.vault.Unlocked() {
		return PhotoSelectionView{}, vault.ErrLocked
	}
	versions := []selectedPhotoVersion{}
	hidden := options.Filter.Hidden || options.Mode == "hidden"
	if options.Search {
		options.Filter.Hidden = hidden
	}
	var generation uint64
	if len(options.IDs) != 0 {
		if len(options.IDs) > 100000 {
			return PhotoSelectionView{}, ErrPhotoSearch
		}
		l.mu.Lock()
		err := l.ensurePhotoIndexLocked(ctx)
		if err != nil {
			l.mu.Unlock()
			return PhotoSelectionView{}, err
		}
		l.photoMu.Lock()
		seen := make(map[string]bool)
		for _, id := range options.IDs {
			file, ok := l.photoByID[id]
			if !ok || file.Folder || file.PhotoParentID != "" || !photoMedia(file.Path) || l.photoPathHiddenLocked(file.Path) != hidden {
				l.photoMu.Unlock()
				l.mu.Unlock()
				return PhotoSelectionView{}, catalog.ErrNotFound
			}
			if !seen[id] {
				versions = append(versions, selectedPhotoVersion{ID: id, Revision: file.Revision})
				seen[id] = true
			}
		}
		generation = l.photoEpoch
		l.photoMu.Unlock()
		l.mu.Unlock()
	} else {
		cursor := ""
		for {
			if err := ctx.Err(); err != nil {
				return PhotoSelectionView{}, err
			}
			var page PhotoPage
			var err error
			if options.Search {
				options.Filter.Cursor, options.Filter.Limit = cursor, PhotoPageMaximum
				page, err = l.PhotoSearchPage(ctx, options.Filter)
			} else {
				page, err = l.PhotoTimelinePage(ctx, PhotoPageMaximum, cursor, options.Filter.Album, options.Mode, options.Date)
			}
			if err != nil {
				return PhotoSelectionView{}, err
			}
			if generation != 0 && generation != page.Generation {
				return PhotoSelectionView{}, ErrPhotoCursorStale
			}
			generation = page.Generation
			for _, item := range page.Items {
				versions = append(versions, selectedPhotoVersion{ID: item.ID, Revision: item.Revision})
			}
			if len(versions) > 100000 {
				return PhotoSelectionView{}, ErrPhotoSearch
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	if len(versions) == 0 {
		return PhotoSelectionView{}, ErrArchiveSelectionEmpty
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.photoMu.Lock()
	unchanged := generation == l.photoEpoch
	l.photoMu.Unlock()
	if !unchanged {
		return PhotoSelectionView{}, ErrPhotoCursorStale
	}
	if err := os.MkdirAll(l.photoSelectionDir(), 0700); err != nil {
		return PhotoSelectionView{}, err
	}
	entries, err := os.ReadDir(l.photoSelectionDir())
	if err != nil {
		return PhotoSelectionView{}, err
	}
	active := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return PhotoSelectionView{}, err
		}
		if time.Since(info.ModTime()) >= PhotoSelectionLifetime {
			if err := os.Remove(filepath.Join(l.photoSelectionDir(), entry.Name())); err != nil {
				return PhotoSelectionView{}, err
			}
		} else {
			active++
		}
	}
	if active >= 128 {
		return PhotoSelectionView{}, errors.New("too many active photo selections; wait for an older selection to expire")
	}
	raw, err := cryptox.Random(16)
	if err != nil {
		return PhotoSelectionView{}, err
	}
	view := PhotoSelectionView{ID: hex.EncodeToString(raw), Count: len(versions), Hidden: hidden, ExpiresAt: time.Now().UTC().Add(PhotoSelectionLifetime)}
	plain, err := json.Marshal(photoSelectionManifest{PhotoSelectionView: view, Versions: versions})
	if err != nil {
		return PhotoSelectionView{}, err
	}
	defer clear(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return PhotoSelectionView{}, err
	}
	if err := cryptox.AtomicWrite(filepath.Join(l.photoSelectionDir(), view.ID+".enc"), wrapped, 0600); err != nil {
		return PhotoSelectionView{}, err
	}
	return view, nil
}

// Requires Library.mu. Revalidation happens before the action captures its
// immutable references or mutates membership; visibility changes fail closed.
func (l *Library) resolvePhotoSelectionLocked(ctx context.Context, id string, hidden bool) ([]catalog.File, error) {
	if !l.vault.Unlocked() {
		return nil, vault.ErrLocked
	}
	if !validSelectionID(id) {
		return nil, catalog.ErrNotFound
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(l.photoSelectionDir(), id+".enc"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil || len(raw) >= 32<<20 {
		return nil, ErrPhotoSearch
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var manifest photoSelectionManifest
	if json.Unmarshal(plain, &manifest) != nil || manifest.ID != id || manifest.Hidden != hidden || manifest.Count != len(manifest.Versions) || manifest.Count < 1 || manifest.Count > 100000 || !time.Now().Before(manifest.ExpiresAt) {
		return nil, ErrPhotoCursorStale
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	files := make([]catalog.File, 0, len(manifest.Versions))
	for _, version := range manifest.Versions {
		file, ok := l.photoByID[version.ID]
		if !ok || file.Folder || file.PhotoParentID != "" || file.Revision != version.Revision || l.photoPathHiddenLocked(file.Path) != hidden {
			return nil, ErrPhotoCursorStale
		}
		files = append(files, file)
	}
	return files, nil
}

func (l *Library) ResolvePhotoSelection(ctx context.Context, id string, hidden bool) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, id, hidden)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.EntryID
	}
	return ids, nil
}
