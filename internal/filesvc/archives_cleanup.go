package filesvc

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *ArchiveManager) cleanupLocked(now time.Time) {
	for id, job := range m.jobs {
		if now.Before(job.ExpiresAt) {
			continue
		}
		job.cancel()
		_ = os.Remove(job.path)
		if job.release != nil {
			job.release()
			job.release = nil
		}
		delete(m.jobs, id)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".archive-") || filepath.Ext(entry.Name()) != ".zip" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if _, ok := m.jobs[id]; !ok {
			_ = os.Remove(filepath.Join(m.root, entry.Name()))
		}
	}
}

func (m *ArchiveManager) cleanupStaleTemps() {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".archive-") {
			_ = os.Remove(filepath.Join(m.root, entry.Name()))
		}
	}
}

func archiveID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
