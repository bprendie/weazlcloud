package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"strings"
)

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
		if inPhotoRoot(f.Path) {
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
			wasPhoto, isPhoto := inPhotoRoot(oldPath), inPhotoRoot(newPath)
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
	case "photo-album":
		changed = true
	case "put", "copy", "restore", "mkdir", "photo-metadata", "photo-visibility", "photo-live":
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
		l.photoQueryCache, l.photoQueryOrder = nil, nil
		l.photoSortedEpoch = 0
		l.schedulePhotoIndexSaveLocked()
	}
}

func (l *Library) replacePhotoLocked(name string, file catalog.File, found bool) bool {
	return l.putPhotoPathLocked(name, file, found && inPhotoRoot(file.Path))
}

func inPhotoRoot(name string) bool {
	return name == strings.TrimSuffix(PhotosRoot, "/") || strings.HasPrefix(name, PhotosRoot)
}
