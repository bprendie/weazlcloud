package filesvc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *ArchiveManager) CleanupExpired(now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupLocked(now)
}

func (m *ArchiveManager) cleanupLocked(now time.Time) (int64, error) {
	var reclaimed int64
	for id, job := range m.jobs {
		expired := !now.Before(job.ExpiresAt)
		terminal := job.Status == "cancelled" || job.Status == "failed"
		if !expired && !terminal {
			continue
		}
		select {
		case <-job.done:
		default:
			if expired {
				job.cancel()
			}
			continue
		}
		size, err := removeArchive(job.path)
		if err != nil {
			return reclaimed, err
		}
		reclaimed += size
		if job.release != nil {
			job.release()
			job.release = nil
		}
		delete(m.jobs, id)
	}
	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return reclaimed, nil
	}
	if err != nil {
		return reclaimed, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".archive-") {
			path := filepath.Join(m.root, entry.Name())
			info, statErr := os.Stat(path)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return reclaimed, statErr
			}
			size := info.Size()
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return reclaimed, removeErr
			}
			reclaimed += size
			continue
		}
		if filepath.Ext(entry.Name()) != ".zip" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if _, ok := m.jobs[id]; !ok {
			path := filepath.Join(m.root, entry.Name())
			info, statErr := os.Stat(path)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return reclaimed, statErr
			}
			if now.Sub(info.ModTime()) < archiveLifetime {
				continue
			}
			size, removeErr := removeArchive(path)
			if removeErr != nil {
				return reclaimed, removeErr
			}
			reclaimed += size
		}
	}
	return reclaimed, nil
}

func removeArchive(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return info.Size(), nil
}

func archiveID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
