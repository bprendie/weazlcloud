package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CleanupThumbnailCache re-applies the cache bounds and removes abandoned
// atomic-write temp files after the node has been idle.
func CleanupThumbnailCache(dir string) (int64, error) {
	before, err := cacheBytes(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".thumbnail-") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	if err := evictThumbnailCache(dir); err != nil {
		return 0, err
	}
	after, err := cacheBytes(dir)
	if err != nil {
		return 0, err
	}
	if before > after {
		return before - after, nil
	}
	return 0, nil
}

func cacheBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
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
	return total, err
}
