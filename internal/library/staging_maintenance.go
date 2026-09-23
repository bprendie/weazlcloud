package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (l *Library) RecoverStaging(ctx context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	before, err := stagingBytes(l.repo)
	if err != nil {
		return 0, err
	}
	if err := l.ensure(ctx); err != nil {
		return 0, err
	}
	after, err := stagingBytes(l.repo)
	if err != nil {
		return 0, err
	}
	if before > after {
		return before - after, nil
	}
	return 0, nil
}

func stagingBytes(repo string) (int64, error) {
	paths := []string{filepath.Join(repo, ".staging")}
	roots, err := filepath.Glob(filepath.Join(filepath.Dir(repo), ".weazl-batch-*"))
	if err != nil {
		return 0, err
	}
	paths = append(paths, roots...)
	var total int64
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if d.Type().IsRegular() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
