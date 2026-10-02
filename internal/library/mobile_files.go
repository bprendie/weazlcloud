package library

import (
	"context"
	"io"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// MobileFileItem intentionally omits backend references and global object hashes.
type MobileFileItem struct {
	ID       string    `json:"id"`
	Revision uint64    `json:"revision"`
	Path     string    `json:"path"`
	Folder   bool      `json:"folder"`
	Size     int64     `json:"size"`
	Mtime    time.Time `json:"mtime"`
}

func mobileFileItem(f catalog.File) MobileFileItem {
	return MobileFileItem{f.EntryID, f.Revision, f.Path, f.Folder, f.Size, f.Mtime}
}

func (l *Library) MobileFileMetadata(ctx context.Context, id string) (MobileFileItem, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return MobileFileItem{}, err
	}
	f, err := l.catalog.MobileFile(id)
	return mobileFileItem(f), err
}

// ReadMobileFile holds the immutable bytes before releasing the mutation lock.
// The callback's metadata and source always describe the same captured revision.
func (l *Library) ReadMobileFile(ctx context.Context, id string, read func(MobileFileItem, PhotoRangeSource) error) error {
	ctx, done := l.previewContext(ctx)
	defer done()
	l.mu.Lock()
	f, ref, release, err := l.captureMobileFileLocked(ctx, id)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	defer release()
	if read == nil {
		return catalog.ErrNotFound
	}
	return read(mobileFileItem(f), func(offset, length int64, dst io.Writer) error {
		if offset < 0 || length < 0 || offset > f.Size || length > f.Size-offset {
			return io.ErrUnexpectedEOF
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return l.readReferenceRange(ctx, ref, offset, length, dst)
	})
}

func (l *Library) captureMobileFileLocked(ctx context.Context, id string) (catalog.File, catalog.Reference, func(), error) {
	var ref catalog.Reference
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, ref, nil, err
	}
	f, err := l.catalog.MobileFile(id)
	if err != nil {
		return f, ref, nil, err
	}
	if f.Folder {
		return f, ref, nil, catalog.ErrNotFound
	}
	ref, err = l.capture(f)
	if err != nil {
		return f, ref, nil, err
	}
	release, err := l.holdReference(ref)
	return f, ref, release, err
}
