package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) SetPreviewLease(enter func(context.Context) (context.Context, func(), bool)) {
	l.activityMu.Lock()
	l.previewLease = enter
	l.activityMu.Unlock()
}

func (l *Library) previewContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	if parent.Err() != nil {
		cancel()
	}
	session := l.vault.Session()
	stop := context.AfterFunc(session, cancel)
	stopLibrary := context.AfterFunc(l.previewLifetime, cancel)
	if l.previewLifetime.Err() != nil {
		cancel()
	}
	if session.Err() != nil {
		cancel()
	}
	l.activityMu.RLock()
	enter := l.previewLease
	l.activityMu.RUnlock()
	release := func() {}
	if enter != nil {
		var ok bool
		ctx, release, ok = enter(ctx)
		if !ok {
			cancel()
		}
	}
	ctx = context.WithValue(ctx, previewSessionKey{}, session)
	return previewSessionContext{ctx, session, l.previewLifetime}, func() { stopLibrary(); stop(); cancel(); release() }
}

type previewSessionKey struct{}

type previewSessionContext struct {
	context.Context
	session, lifetime context.Context
}

func (c previewSessionContext) Err() error {
	if err := c.session.Err(); err != nil {
		return err
	}
	if err := c.lifetime.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}

func (l *Library) validatePreview(ctx context.Context, f catalog.File) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.validatePreviewLocked(ctx, f)
}

func (l *Library) validatePreviewLocked(ctx context.Context, f catalog.File) error {
	if session, ok := ctx.Value(previewSessionKey{}).(context.Context); ok && session.Err() != nil {
		return session.Err()
	}
	if err := l.previewLifetime.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !l.vault.Unlocked() {
		return ErrThumbnailUnavailable
	}
	current, ok := l.catalog.Get(f.Path)
	if !ok || current.EntryID != f.EntryID || current.Revision != f.Revision || current.Hash != f.Hash {
		return ErrThumbnailUnavailable
	}
	return nil
}
