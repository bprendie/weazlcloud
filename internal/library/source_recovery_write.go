package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) AdoptPhotoSourceCollection(ctx context.Context, device string, a catalog.SourceRecoveryAdoption, guard func(func() error) error) (catalog.SourceMapping, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return catalog.SourceMapping{}, err
	}
	if err := l.ensure(ctx); err != nil {
		return catalog.SourceMapping{}, err
	}
	var mapping catalog.SourceMapping
	publish := func() error {
		var err error
		mapping, err = l.catalog.AdoptSourceCollection(device, a)
		return err
	}
	if guard == nil {
		return catalog.SourceMapping{}, catalog.ErrSourceConflict
	}
	if err := guard(publish); err != nil {
		return catalog.SourceMapping{}, err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return mapping, nil
}
