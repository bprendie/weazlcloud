package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (l *Library) ValidatePhotoUploadAlbums(ctx context.Context, ids []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return err
	}
	known := make(map[string]bool)
	for _, album := range l.catalog.Albums() {
		known[album.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return catalog.ErrAlbumNotFound
		}
	}
	return nil
}
