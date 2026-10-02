package library

import (
	"context"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// PhotoTrash applies the same inherited visibility rules to deleted media as
// the live timeline. Trashed parent flags remain authoritative until purged.
func (l *Library) PhotoTrash(ctx context.Context, hidden bool) ([]catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return nil, vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	all := l.catalog.All()
	folders := photoTrashHiddenFolders(all)
	out := make([]catalog.File, 0)
	for _, file := range all {
		if photoTrashVisible(file, folders, hidden) {
			out = append(out, file)
		}
	}
	return out, nil
}

func (l *Library) RestorePhoto(ctx context.Context, id string, hidden bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return err
	}
	all := l.catalog.All()
	folders := photoTrashHiddenFolders(all)
	for _, file := range all {
		if file.EntryID == id && photoTrashVisible(file, folders, hidden) {
			// Restore the deleted parent first so inherited hidden state cannot
			// disappear when only a child is returned to the live index.
			for _, parent := range all {
				if parent.Folder && !parent.Present && strings.HasPrefix(file.Path, parent.Path+"/") {
					return catalog.ErrConflict
				}
			}
			return l.restoreLocked(ctx, file.Path)
		}
	}
	return catalog.ErrNotFound
}

func photoTrashHiddenFolders(all []catalog.File) map[string]bool {
	folders := make(map[string]bool)
	for _, file := range all {
		if file.Hidden {
			folders[file.Path] = true
		}
	}
	return folders
}

func photoTrashVisible(file catalog.File, folders map[string]bool, hidden bool) bool {
	return !file.Present && file.DeletedAt != nil && strings.HasPrefix(file.Path, PhotosRoot) &&
		(file.Folder || file.PhotoParentID == "" && photoMedia(file.Path)) && photoPathHidden(file.Path, folders) == hidden
}
