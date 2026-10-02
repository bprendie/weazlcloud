package library

import (
	"context"
	"fmt"
	"github.com/bprendie/weazlcloud/internal/catalog"
)

// ChangePhotoSelectionVisibility validates the entire selection before one save.
func (l *Library) ChangePhotoSelectionVisibility(ctx context.Context, selection string, hiddenView bool, action string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, selection, hiddenView)
	if err != nil {
		return 0, err
	}
	count := len(files)
	var hidden, archived *bool
	value := action == "hide" || action == "set_archived"
	switch action {
	case "hide", "unhide":
		hidden = &value
	case "set_archived", "unarchive":
		archived = &value
	default:
		return 0, ErrPhotoSearch
	}
	// Hiding an asset also hides its paired motion/original components.
	if hidden != nil {
		l.photoMu.Lock()
		seen := make(map[string]bool)
		for _, file := range files {
			seen[file.EntryID] = true
		}
		originals := append([]catalog.File(nil), files...)
		folders := l.hiddenPhotoFoldersOnlyLocked()
		for _, file := range originals {
			// A child cannot override a hidden parent folder. Restore that folder first.
			if !value && photoPathHidden(file.Path, folders) {
				l.photoMu.Unlock()
				return 0, fmt.Errorf("%w: unhide the containing folder first", ErrPhotoSearch)
			}
			for _, part := range file.PhotoComponents {
				component, ok := l.photoByID[part.AssetID]
				if ok && component.Present && !component.Folder && !seen[component.EntryID] {
					files = append(files, component)
					seen[component.EntryID] = true
				}
			}
		}
		if !value {
			for _, file := range files {
				if photoPathHidden(file.Path, folders) {
					l.photoMu.Unlock()
					return 0, fmt.Errorf("%w: unhide the containing folder first", ErrPhotoSearch)
				}
			}
		}
		l.photoMu.Unlock()
	}
	changed, err := l.catalog.SetPhotoFlags(files, hidden, archived)
	if err != nil {
		return 0, err
	}
	paths := make([]string, len(changed))
	for i, file := range changed {
		paths[i] = file.Path
	}
	if len(paths) != 0 {
		l.publishChange(Change{Kind: "photo-visibility", Paths: paths})
	}
	return count, nil
}

// Requires photoMu; exact asset flags must not prevent unhiding the asset.
func (l *Library) hiddenPhotoFoldersOnlyLocked() map[string]bool {
	folders := make(map[string]bool)
	for _, file := range l.photoRows {
		if file.Folder && file.Hidden {
			folders[file.Path] = true
		}
	}
	return folders
}
