package library

import (
	"context"
	"io"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type PhotoRangeSource func(offset, length int64, dst io.Writer) error

// ReadPhotoOriginal authorizes one immutable reference and releases the metadata
// lock before streaming. Large video/disk-backed reads cannot block all browsing.
func (l *Library) ReadPhotoOriginal(ctx context.Context, id string, hidden bool, read func(PhotoItem, PhotoRangeSource) error) error {
	ctx, done := l.previewContext(ctx)
	defer done()
	l.mu.Lock()
	if !l.vault.Unlocked() {
		l.mu.Unlock()
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		l.mu.Unlock()
		return err
	}
	l.photoMu.Lock()
	file, ok := l.photoByID[id]
	visible := ok && !file.Folder && photoMedia(file.Path) && l.photoPathHiddenLocked(file.Path) == hidden
	l.photoMu.Unlock()
	if !visible {
		l.mu.Unlock()
		return catalog.ErrNotFound
	}
	ref, err := l.capture(file)
	if err != nil {
		l.mu.Unlock()
		return err
	}
	release, err := l.holdReference(ref)
	item := l.photoItemVisible(file, hidden)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	defer release()
	if read == nil {
		return ErrPhotoCursor
	}
	return read(item, func(offset, length int64, dst io.Writer) error {
		if offset < 0 || length < 0 || offset > file.Size || length > file.Size-offset {
			return ErrPhotoCursor
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return l.readReferenceRange(ctx, ref, offset, length, dst)
	})
}
