package library

import (
	"context"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

func (l *Library) Dedupe(ctx context.Context) (int, int64, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, 0, 0, err
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
	if err := l.recoverStaged(ctx); err != nil {
		return 0, err
	}
	expired, protected, candidates := trashSnapshotPlan(l.catalog.All(), before)
	if len(expired) == 0 {
		return 0, nil
	}
	forget := make([]string, 0)
	if len(candidates) > 0 {
		pass, _, err := l.vault.Secrets()
		if err != nil {
			return 0, err
		}
		snapshots, err := l.restic.Snapshots(ctx, restic.Repo{Location: l.repo, Password: pass})
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
	removed, err := l.catalog.PurgeTrash(before)
	if err != nil {
		return 0, err
	}
	if len(forget) > 0 {
		pass, _, err := l.vault.Secrets()
		if err != nil {
			return 0, err
		}
		if err := l.restic.Forget(ctx, restic.Repo{Location: l.repo, Password: pass}, forget); err != nil {
			return 0, err
		}
	}
	l.publishChange(Change{Kind: "trash-purge"})
	var removedBytes int64
	for _, f := range removed {
		if !f.Folder {
			removedBytes += f.Size
		}
	}
	return removedBytes, nil
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
