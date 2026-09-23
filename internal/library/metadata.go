package library

import (
	"context"
	"errors"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) List() []catalog.File {
	return l.catalog.List()
}

func (l *Library) Usage(ctx context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, err
	}
	var total int64
	for _, f := range l.catalog.List() {
		total += f.Size
	}
	return total, nil
}

func (l *Library) Metadata(ctx context.Context, name string) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	f, ok := l.catalog.Get(name)
	if !ok {
		return catalog.File{}, errors.New("file is not in the library")
	}
	return f, nil
}
