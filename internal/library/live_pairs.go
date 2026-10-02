package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"strings"
)

func (l *Library) LinkLivePhoto(ctx context.Context, stillID, motionID string, stillRevision, motionRevision uint64, hidden, unlink bool) (PhotoItem, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoItem{}, err
	}
	l.photoMu.Lock()
	a, okA := l.photoByID[stillID]
	b, okB := l.photoByID[motionID]
	visible := okA && okB && l.photoPathHiddenLocked(a.Path) == hidden && l.photoPathHiddenLocked(b.Path) == hidden
	l.photoMu.Unlock()
	if !visible {
		return PhotoItem{}, catalog.ErrNotFound
	}
	if !strings.HasPrefix(photoMediaType(a.Path), "image/") || !strings.HasPrefix(photoMediaType(b.Path), "video/") {
		return PhotoItem{}, catalog.ErrConflict
	}
	updated, err := l.catalog.SetLivePhotoPair(stillID, motionID, stillRevision, motionRevision, unlink)
	if err != nil {
		return PhotoItem{}, err
	}
	l.publishChange(Change{Kind: "photo-live", Paths: []string{a.Path, b.Path}})
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoItem{}, err
	}
	return l.photoItemVisible(updated, hidden), nil
}
