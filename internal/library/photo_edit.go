package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/vault"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type PhotoUpdate struct {
	Rotation *int
	Favorite *bool
	Archived *bool
	Caption  *string
	Capture  *catalog.CaptureMetadata
}

func (l *Library) UpdatePhoto(ctx context.Context, entryID string, update PhotoUpdate, hiddenView bool) (PhotoItem, error) {
	if update.Rotation == nil && update.Favorite == nil && update.Archived == nil && update.Caption == nil && update.Capture == nil {
		return PhotoItem{}, ErrPhotoCursor
	}
	if update.Rotation != nil && (*update.Rotation < 0 || *update.Rotation > 270 || *update.Rotation%90 != 0) {
		return PhotoItem{}, ErrPhotoSearch
	}
	if update.Caption != nil && len(*update.Caption) > 4096 {
		return PhotoItem{}, ErrPhotoSearch
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoItem{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoItem{}, err
	}
	l.photoMu.Lock()
	indexed, ok := l.photoByID[entryID]
	hidden := ok && l.photoPathHiddenLocked(indexed.Path)
	l.photoMu.Unlock()
	if !ok || indexed.Folder || !photoMedia(indexed.Path) || hidden != hiddenView {
		return PhotoItem{}, catalog.ErrNotFound
	}
	current, ok := l.catalog.Get(indexed.Path)
	if !ok || current.EntryID != entryID || current.Revision != indexed.Revision {
		return PhotoItem{}, ErrPhotoCursorStale
	}
	media := catalog.MediaMetadata{Width: current.Width, Height: current.Height, DurationMillis: current.DurationMillis, Orientation: current.Orientation, Favorite: current.Favorite, Archived: current.Archived, Caption: current.Caption, UserRotation: current.UserRotation, Camera: current.Camera}
	if update.Rotation != nil {
		media.UserRotation = *update.Rotation
	}
	if update.Favorite != nil {
		media.Favorite = *update.Favorite
	}
	if update.Archived != nil {
		media.Archived = *update.Archived
	}
	if update.Caption != nil {
		media.Caption = *update.Caption
	}
	updated, err := l.catalog.UpdatePhotoUserMetadata(entryID, current.Revision, update.Capture, media)
	if err != nil {
		return PhotoItem{}, err
	}
	l.publishChange(Change{Kind: "photo-metadata", Paths: []string{updated.Path}})
	return l.photoItemVisible(updated, hiddenView), nil
}
