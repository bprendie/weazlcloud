package filesvc

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
)

func (m *ArchiveManager) OpenDownload(ctx context.Context, id string) (io.ReadSeekCloser, error) {
	m.mu.Lock()
	if err := m.loadJobsLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	job := m.jobs[id]
	if job == nil || job.Status != "ready" || !time.Now().Before(job.ExpiresAt) {
		m.mu.Unlock()
		return nil, os.ErrNotExist
	}
	path, size := job.path, job.zipBytes
	m.mu.Unlock()
	if strings.HasSuffix(path, ".zip") {
		return os.Open(path)
	}
	return m.lib.OpenEncryptedArchive(ctx, id, path, size)
}
