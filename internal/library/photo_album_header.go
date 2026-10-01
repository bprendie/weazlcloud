package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Mutation responses never send an entire large album or hidden memberships
// outside the explicit context. Reconcile the remainder through memberships.
func (l *Library) PhotoAlbumHeader(ctx context.Context, id string, hidden bool) (PhotoAlbum, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoAlbum{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoAlbum{}, err
	}
	for _, album := range l.catalog.Albums() {
		if album.ID == id {
			l.photoMu.Lock()
			defer l.photoMu.Unlock()
			view := l.syncAlbumLocked(album, hidden)
			if len(view.AssetIDs) > 200 {
				view.AssetIDs = view.AssetIDs[:200]
			}
			return view, nil
		}
	}
	return PhotoAlbum{}, catalog.ErrAlbumNotFound
}
