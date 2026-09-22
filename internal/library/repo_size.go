package library

import (
	"io/fs"
	"path/filepath"
	"syscall"
)

func repositoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Blocks > 0 {
				total += stat.Blocks * 512
			} else {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func reclaimedRepositoryBytes(root string, before int64) (int64, error) {
	after, err := repositoryBytes(root)
	if err != nil {
		return 0, err
	}
	if before <= after {
		return 0, nil
	}
	return before - after, nil
}
