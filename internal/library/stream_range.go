package library

import (
	"context"
	"errors"
	"io"
)

// StreamRange reads a byte range without retaining the complete file.
func (l *Library) StreamRange(ctx context.Context, name string, offset, length int64, w io.Writer) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	f, ok := l.catalog.Get(name)
	if !ok || f.Folder || offset < 0 || length < 0 || offset > f.Size || length > f.Size-offset {
		return errors.New("invalid file range")
	}
	ref, err := l.capture(f)
	if err != nil {
		return err
	}
	return l.readReferenceRange(ctx, ref, offset, length, w)
}
