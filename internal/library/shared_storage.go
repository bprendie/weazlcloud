package library

import (
	"context"
	"errors"
	"io"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

// ConfigureShared enables mixed reads and optional experimental shared writes for one owner.
func (l *Library) ConfigureShared(owner string, store *sharedstore.Store, writeEnabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ownerID, l.sharedStore, l.sharedWrites = owner, store, writeEnabled
}

func (l *Library) SharedWritesEnabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sharedStore != nil && l.sharedWrites
}

func (l *Library) SharedMetrics(ctx context.Context) (shared bool, allocated, manifests, index, staging int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sharedStore == nil {
		return false, 0, 0, 0, 0, nil
	}
	stats, err := l.sharedStore.Metrics(ctx)
	return true, stats.AllocatedBytes, stats.ManifestAllocated, stats.IndexAllocatedBytes, stats.StagingAllocated, err
}

func (l *Library) capture(file catalog.File) (catalog.Reference, error) {
	if file.Reference != nil && file.Reference.Backend == catalog.SharedBackend {
		if l.sharedStore == nil || l.ownerID == "" {
			return catalog.Reference{}, catalog.ErrUnknownReference
		}
		if err := catalog.ValidateFileReference(file); err != nil {
			return catalog.Reference{}, err
		}
		return *file.Reference, nil
	}
	return l.backend.Capture(file)
}

func (l *Library) readReference(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	if ref.Backend != catalog.SharedBackend {
		return l.backend.Read(ctx, ref, w)
	}
	if l.sharedStore == nil || l.ownerID == "" {
		return catalog.ErrUnknownReference
	}
	return l.sharedStore.Read(ctx, l.ownerID, l.vault, toSharedReference(ref), w)
}

func (l *Library) readReferenceRange(ctx context.Context, ref catalog.Reference, offset, length int64, w io.Writer) error {
	if ref.Backend != catalog.SharedBackend {
		return l.backend.ReadRange(ctx, ref, offset, length, w)
	}
	if offset < 0 || length < 0 {
		return errors.New("invalid storage range")
	}
	if length == 0 {
		return nil
	}
	ranged := &rangeWriter{offset: offset, length: length, output: w}
	if err := l.readReference(ctx, ref, ranged); err != nil {
		return err
	}
	if ranged.written != length {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (l *Library) holdReference(ref catalog.Reference) (func(), error) {
	if ref.Backend != catalog.SharedBackend {
		return l.backend.Hold(ref)
	}
	if l.sharedStore == nil || l.ownerID == "" {
		return nil, catalog.ErrUnknownReference
	}
	return l.sharedStore.Hold(context.Background(), l.ownerID, toSharedReference(ref))
}

func (l *Library) releaseReference(ctx context.Context, ref *catalog.Reference) error {
	if ref == nil || ref.Backend != catalog.SharedBackend {
		return nil
	}
	if l.sharedStore == nil {
		return catalog.ErrUnknownReference
	}
	return l.sharedStore.Release(ctx, l.ownerID, ref.OwnerEntryID, ref.OwnerRevision)
}

// ReconcileShared resolves interrupted storage operations against this owner's
// authenticated catalog, including references retained in Trash.
func (l *Library) ReconcileShared(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sharedStore == nil || l.ownerID == "" {
		return nil
	}
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.recoverStaged(ctx); err != nil {
		return err
	}
	published := make(map[string]struct{})
	for _, file := range l.catalog.All() {
		if file.Reference != nil && file.Reference.Backend == catalog.SharedBackend {
			published[file.Reference.Operation] = struct{}{}
		}
	}
	return l.sharedStore.ReconcileOwner(ctx, l.ownerID, published)
}

func toSharedReference(ref catalog.Reference) sharedstore.Reference {
	return sharedstore.Reference{Version: int(ref.Version), ObjectID: ref.Object, EntryID: ref.OwnerEntryID, Revision: ref.OwnerRevision, Operation: ref.Operation}
}
