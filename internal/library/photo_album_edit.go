package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/vault"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) SavePhotoAlbum(ctx context.Context, album catalog.Album) (catalog.Album, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.Album{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.Album{}, err
	}
	updated, err := l.catalog.SaveAlbum(album)
	if err != nil {
		return catalog.Album{}, err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return updated, nil
}

func (l *Library) DeletePhotoAlbum(ctx context.Context, id string, revision uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return err
	}
	if err := l.catalog.DeleteAlbum(id, revision); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return nil
}

func (l *Library) ChangePhotoAlbumMembers(ctx context.Context, id string, revision uint64, add, remove []string) (catalog.Album, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.Album{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.Album{}, err
	}
	updated, err := l.catalog.ChangeAlbumMembers(id, revision, add, remove)
	if err != nil {
		return catalog.Album{}, err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return updated, nil
}
