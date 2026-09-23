package library

import (
	"context"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) Dedupe(ctx context.Context) (int, int64, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, 0, 0, err
	}
	if l.sharedStore != nil {
		stats, err := l.sharedStore.Metrics(ctx)
		if err != nil {
			return 0, 0, 0, err
		}
		logical, unique := stats.LogicalBytes, stats.UniqueBytes
		percent := 0
		if logical > 0 {
			percent = int((logical - unique) * 100 / logical)
		}
		return percent, logical, unique, nil
	}
	logical := int64(0)
	unique := int64(0)
	hashes := make(map[string]int64)
	for _, f := range l.catalog.List() {
		if f.Folder {
			continue
		}
		logical += f.Size
		if _, ok := hashes[f.Hash]; !ok {
			hashes[f.Hash] = f.Size
			unique += f.Size
		}
	}
	percent := 0
	if logical > 0 {
		percent = int((logical - unique) * 100 / logical)
	}
	return percent, logical, unique, nil
}

func (l *Library) Trash(ctx context.Context) ([]catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	return l.catalog.Trash(), nil
}

func (l *Library) Restore(ctx context.Context, name string) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.resumeTrashCleanup(ctx); err != nil {
		return err
	}
	if err := l.catalog.Restore(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "restore", Paths: []string{name}})
	return nil
}

func (l *Library) CleanupTrash(ctx context.Context, before time.Time) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, err
	}
	initialBytes, err := repositoryBytes(l.repo)
	if err != nil {
		return 0, err
	}
	if err := l.recoverStaged(ctx); err != nil {
		return 0, err
	}
	pendingIntent, err := l.loadTrashIntent()
	if err != nil {
		return 0, err
	}
	if err := l.resumeTrashCleanup(ctx); err != nil {
		return 0, err
	}
	if pendingIntent.Version != 0 {
		l.publishChange(Change{Kind: "trash-purge"})
	}
	expired, protected, candidates := trashSnapshotPlan(l.catalog.All(), before)
	if len(expired) == 0 {
		return reclaimedRepositoryBytes(l.repo, initialBytes)
	}
	forget := make([]string, 0)
	if len(candidates) > 0 {
		snapshots, err := l.backend.Snapshots(ctx)
		if err != nil {
			return 0, err
		}
		for _, snap := range snapshots {
			if _, candidate := candidates[snap]; !candidate {
				continue
			}
			if _, stillNeeded := protected[snap]; !stillNeeded {
				forget = append(forget, snap)
			}
		}
	}
	var sharedRefs []catalog.Reference
	if l.sharedStore != nil {
		for _, f := range expired {
			if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
				sharedRefs = append(sharedRefs, *f.Reference)
			}
		}
	}
	var intent trashCleanupIntent
	if len(forget) > 0 || len(sharedRefs) > 0 {
		intent = trashCleanupIntent{Version: 1, Before: before, Snapshots: forget, SharedReferences: sharedRefs}
		if err := l.saveTrashIntent(intent); err != nil {
			return 0, err
		}
	}
	if _, err := l.catalog.PurgeTrash(before); err != nil {
		return 0, err
	}
	if intent.Version != 0 {
		intent.CatalogPurged = true
		if err := l.saveTrashIntent(intent); err != nil {
			return 0, err
		}
		if err := l.resumeTrashCleanup(ctx); err != nil {
			return 0, err
		}
	}
	l.publishChange(Change{Kind: "trash-purge"})
	freed, err := reclaimedRepositoryBytes(l.repo, initialBytes)
	if err != nil {
		return 0, err
	}
	if l.sharedStore != nil {
		sharedFreed, e := l.sharedStore.Collect(ctx)
		if e != nil {
			return freed, e
		}
		freed += sharedFreed
	}
	return freed, nil
}

func trashSnapshotPlan(all []catalog.File, before time.Time) ([]catalog.File, map[string]struct{}, map[string]struct{}) {
	var expired []catalog.File
	protected := make(map[string]struct{})
	candidates := make(map[string]struct{})
	for _, file := range all {
		isExpired := !file.Present && file.DeletedAt != nil && !file.DeletedAt.After(before)
		if isExpired {
			expired = append(expired, file)
			if !file.Folder && file.Snap != "" {
				candidates[file.Snap] = struct{}{}
			}
			continue
		}
		if file.Snap != "" {
			protected[file.Snap] = struct{}{}
		}
	}
	return expired, protected, candidates
}
