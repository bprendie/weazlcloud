package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) LookupPhotoSourceCollections(ctx context.Context, ids []string, namespace, after string, limit int) (catalog.SourceRecoveryPage, error) {
	if err := catalog.ValidateSourceRecovery(ids, namespace, limit); err != nil {
		return catalog.SourceRecoveryPage{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return catalog.SourceRecoveryPage{}, err
	}
	// Collection recovery needs catalog metadata, not media indexing or rendering.
	if err := l.ensure(ctx); err != nil {
		return catalog.SourceRecoveryPage{}, err
	}
	return l.catalog.LookupSourceCollections(ctx, ids, namespace, after, limit)
}
