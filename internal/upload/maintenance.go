package upload

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

func (m *Manager) SweepExpiredDetailed(ctx context.Context, now time.Time) (int, int64, error) {
	owners, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	removed := 0
	var reclaimed int64
	for _, owner := range owners {
		if !owner.IsDir() || !validComponent(owner.Name()) {
			continue
		}
		files, err := os.ReadDir(m.ownerDir(owner.Name()))
		if err != nil {
			return removed, reclaimed, err
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return removed, reclaimed, err
			}
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".json")
			if !validComponent(id) {
				continue
			}
			unlock := m.lockSession(id)
			paths := []string{m.manifestPath(owner.Name(), id), m.partPath(owner.Name(), id), m.chunkPath(owner.Name(), id)}
			before, err := pathBytes(paths)
			if err != nil {
				unlock()
				return removed, reclaimed, err
			}
			m.mu.Lock()
			s, loadErr := m.readSessionLocked(owner.Name(), id)
			if errors.Is(loadErr, ErrNotFound) {
				m.mu.Unlock()
				unlock()
				continue
			}
			if loadErr != nil {
				m.mu.Unlock()
				unlock()
				return removed, reclaimed, loadErr
			}
			if s.Status == "complete" || now.Sub(s.UpdatedAt) < SessionLifetime {
				m.mu.Unlock()
				unlock()
				continue
			}
			if err := m.removeLocked(s); err != nil {
				m.mu.Unlock()
				unlock()
				return removed, reclaimed, err
			}
			m.releaseReservationLocked(id)
			m.mu.Unlock()
			unlock()
			after, err := pathBytes(paths)
			if err != nil {
				return removed, reclaimed, err
			}
			removed++
			reclaimed += positiveDiff(before, after)
		}
	}
	return removed, reclaimed, nil
}

func pathBytes(paths []string) (int64, error) {
	var total int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return total, err
		}
		total += info.Size()
	}
	return total, nil
}
func positiveDiff(before, after int64) int64 {
	if before > after {
		return before - after
	}
	return 0
}
