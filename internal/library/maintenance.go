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
	all := l.catalog.All()
	protected := make(map[string]struct{})
	var reclaimable int64
	for _, f := range all {
		if f.Present || f.DeletedAt == nil || f.DeletedAt.After(before) {
			if f.Snap != "" {
				protected[f.Snap] = struct{}{}
			}
		}
		if !f.Present && f.DeletedAt != nil && !f.DeletedAt.After(before) && !f.Folder {
			reclaimable += f.Size
		}
	}
	if reclaimable == 0 {
		return 0, nil
	}
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return 0, err
	}
	snapshots, err := l.restic.Snapshots(ctx, restic.Repo{Location: l.repo, Password: pass})
	if err != nil {
		return 0, err
	}
	forget := make([]string, 0)
	for _, snap := range snapshots {
		if _, ok := protected[snap]; !ok {
			forget = append(forget, snap)
		}
	}
	removed, err := l.catalog.PurgeTrash(before)
	if err != nil {
		return 0, err
	}
	if err := l.restic.Forget(ctx, restic.Repo{Location: l.repo, Password: pass}, forget); err != nil {
		return 0, err
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
