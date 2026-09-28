package library

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

const (
	PhotoPageDefault = 100
	PhotoPageMaximum = 200
)

var (
	ErrPhotoCursor      = errors.New("invalid photo cursor")
	ErrPhotoCursorStale = errors.New("photo library changed; reload the page")
)

type PhotoItem struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
	MediaType string    `json:"media_type"`
}

type PhotoPage struct {
	Items      []PhotoItem `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Generation uint64      `json:"generation"`
	IndexReady bool        `json:"index_ready"`
	Indexed    int         `json:"indexed"`
	IndexTotal int         `json:"index_total"`
}

type photoCursor struct {
	Generation uint64 `json:"generation"`
	Offset     int    `json:"offset"`
	Album      string `json:"album,omitempty"`
}

// PhotoPage returns a bounded page from an owner-private in-memory index.
// The first request reads catalog metadata once; it never restores photo bytes.
func (l *Library) PhotoPage(ctx context.Context, limit int, cursor, album string) (PhotoPage, error) {
	if limit <= 0 {
		limit = PhotoPageDefault
	}
	if limit > PhotoPageMaximum {
		limit = PhotoPageMaximum
	}
	if album != "" {
		clean, err := cleanPath(album)
		if err != nil || !strings.HasPrefix(clean, PhotosRoot) {
			return PhotoPage{}, ErrPhotoCursor
		}
		album = clean
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoPage{}, errors.New("vault is locked")
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoPage{}, err
	}
	offset := 0
	if cursor != "" {
		decoded, err := l.decodePhotoCursor(cursor)
		if err != nil {
			return PhotoPage{}, err
		}
		if decoded.Generation != l.photoGeneration() || decoded.Album != album {
			return PhotoPage{}, ErrPhotoCursorStale
		}
		offset = decoded.Offset
	}
	l.photoMu.Lock()
	if l.photoSortedEpoch != l.photoEpoch {
		sortPhotoRows(l.photoMediaRows)
		l.reindexPhotoMediaLocked()
		l.photoSortedEpoch = l.photoEpoch
	}
	generation := l.photoEpoch
	indexed := len(l.photoRows)
	indexReady := l.photoReady
	pageFiles := make([]catalog.File, 0, limit)
	total := 0
	if album == "" {
		total = len(l.photoMediaRows)
		if offset < 0 || offset > total {
			l.photoMu.Unlock()
			return PhotoPage{}, ErrPhotoCursor
		}
		pageFiles = append(pageFiles, l.photoMediaRows[offset:min(offset+limit, total)]...)
	} else {
		for _, f := range l.photoMediaRows {
			if strings.HasPrefix(f.Path, album+"/") {
				if total >= offset && len(pageFiles) < limit {
					pageFiles = append(pageFiles, f)
				}
				total++
			}
		}
		if offset < 0 || offset > total {
			l.photoMu.Unlock()
			return PhotoPage{}, ErrPhotoCursor
		}
	}
	l.photoMu.Unlock()
	page := PhotoPage{Items: make([]PhotoItem, 0, len(pageFiles)), Generation: generation, IndexReady: indexReady, Indexed: indexed, IndexTotal: indexed}
	for _, f := range pageFiles {
		page.Items = append(page.Items, PhotoItem{ID: f.EntryID, Path: f.Path, Size: f.Size, Modified: f.Mtime, MediaType: photoMediaType(f.Path)})
	}
	if offset+len(pageFiles) < total {
		var err error
		page.NextCursor, err = l.encodePhotoCursor(photoCursor{Generation: generation, Offset: offset + len(pageFiles), Album: album})
		if err != nil {
			return PhotoPage{}, err
		}
	}
	l.resumePhotoPreparation()
	return page, nil
}

func (l *Library) photoIsReady() bool {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	return l.photoReady
}

func (l *Library) photoGeneration() uint64 {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	return l.photoEpoch
}

func (l *Library) buildPhotoIndex() {
	rows := make([]catalog.File, 0)
	for _, f := range l.catalog.List() {
		if strings.HasPrefix(f.Path, PhotosRoot) {
			rows = append(rows, f)
		}
	}
	sortPhotoRows(rows)
	if cached, epoch, ok := l.loadPhotoIndexCache(); ok && samePhotoRows(rows, cached) {
		rows = cached
		if epoch > l.photoEpoch {
			l.photoEpoch = epoch
		}
	}
	l.photoMu.Lock()
	l.photoRows, l.photoReady = rows, true
	l.rebuildPhotoLookupsLocked()
	if l.photoEpoch == 0 {
		l.photoEpoch = 1
	}
	sortPhotoRows(l.photoRows)
	l.photoSortedEpoch = l.photoEpoch
	l.schedulePhotoIndexSaveLocked()
	l.photoMu.Unlock()
}

// ensurePhotoIndexLocked builds once from the authoritative catalog. The caller
// holds Library.mu so the first catalog load/build cannot race a mutation.
func (l *Library) ensurePhotoIndexLocked(ctx context.Context) error {
	if !l.photoIsReady() {
		if err := l.ensure(ctx); err != nil {
			return err
		}
		l.buildPhotoIndex()
	}
	return nil
}

// photoEntriesLocked returns a stable copy for aggregate album calculation.
func (l *Library) photoEntriesLocked(ctx context.Context) ([]catalog.File, error) {
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return nil, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	return append([]catalog.File(nil), l.photoRows...), nil
}

func (l *Library) updatePhotoIndex(change Change) {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	if !l.photoReady || len(change.Paths) == 0 {
		return
	}
	changed := false
	renamed := false
	switch change.Kind {
	case "rename":
		if len(change.Paths) == 2 {
			oldPath, newPath := change.Paths[0], change.Paths[1]
			wasPhoto, isPhoto := strings.HasPrefix(oldPath, PhotosRoot), strings.HasPrefix(newPath, PhotosRoot)
			renamed = true
			for i := range l.photoRows {
				if l.photoRows[i].Path == oldPath || strings.HasPrefix(l.photoRows[i].Path, oldPath+"/") {
					nextPath := newPath + strings.TrimPrefix(l.photoRows[i].Path, oldPath)
					if current, ok := l.catalog.Get(nextPath); ok {
						l.photoRows[i] = current
					} else {
						l.photoRows[i].Path = nextPath
					}
					changed = true
				}
			}
			if !wasPhoto && isPhoto {
				for _, f := range l.catalog.List() {
					if f.Path == newPath || strings.HasPrefix(f.Path, newPath+"/") {
						l.replacePhotoLocked(f.Path, f, true)
						changed = true
					}
				}
			} else if wasPhoto && !isPhoto {
				kept := l.photoRows[:0]
				for _, f := range l.photoRows {
					if f.Path == newPath || strings.HasPrefix(f.Path, newPath+"/") {
						changed = true
						continue
					}
					kept = append(kept, f)
				}
				l.photoRows = kept
			}
		}
	case "delete":
		for _, name := range change.Paths {
			var remove []string
			for _, f := range l.photoRows {
				if f.Path == name || strings.HasPrefix(f.Path, name+"/") {
					remove = append(remove, f.Path)
				}
			}
			for _, path := range remove {
				changed = l.removePhotoPathLocked(path) || changed
			}
		}
	case "put", "copy", "restore", "mkdir":
		for _, name := range change.Paths {
			f, ok := l.catalog.Get(name)
			changed = l.replacePhotoLocked(name, f, ok) || changed
		}
	}
	if changed {
		if renamed {
			l.rebuildPhotoLookupsLocked()
		}
		l.photoEpoch++
		l.photoSortedEpoch = 0
		l.schedulePhotoIndexSaveLocked()
	}
}

func (l *Library) replacePhotoLocked(name string, file catalog.File, found bool) bool {
	return l.putPhotoPathLocked(name, file, found && strings.HasPrefix(file.Path, PhotosRoot))
}
