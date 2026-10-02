package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (l *Library) ImportPhotoSources(ctx context.Context, device string, ops []catalog.SourceOperation, memberships bool) ([]catalog.SourceOutcome, error) {
	return l.ImportPhotoSourcesGuarded(ctx, device, ops, memberships, nil)
}

// Guard serializes device revocation with source mapping and tree publication.
// Lock order is library -> users -> catalog, matching backup publication.
// It must invoke publish synchronously exactly once if authorization is current.
func (l *Library) ImportPhotoSourcesGuarded(ctx context.Context, device string, ops []catalog.SourceOperation, memberships bool, guard func(func() error) error) ([]catalog.SourceOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !l.vault.Unlocked() {
		return nil, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return nil, err
	}
	var outcomes []catalog.SourceOutcome
	publish := func() error {
		var e error
		outcomes, e = l.catalog.ImportSourceOperations(device, ops, memberships)
		return e
	}
	var err error
	if guard != nil {
		err = guard(publish)
	} else {
		err = publish()
	}
	if err == nil {
		l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	}
	return outcomes, err
}
func (l *Library) PhotoSourceMapping(ctx context.Context, device, namespace, id string) (catalog.SourceMapping, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.SourceMapping{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.SourceMapping{}, err
	}
	return l.catalog.SourceMapping(device, namespace, id)
}
