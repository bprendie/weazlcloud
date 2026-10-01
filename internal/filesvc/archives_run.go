package filesvc

import (
	"context"
	"errors"
	"os"
	"time"
)

func (m *ArchiveManager) run(ctx context.Context, job *archiveJob) {
	defer job.manifest.Release()
	defer job.activityRelease()
	defer close(job.done)
	defer m.workers.Done()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		m.mu.Lock()
		job.Status = "cancelled"
		job.Error = "archive cancelled"
		logArchive(job)
		if job.release != nil {
			job.release()
			job.release = nil
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	if ctx.Err() != nil {
		job.Status, job.Error = "cancelled", "archive cancelled"
		if job.release != nil {
			job.release()
			job.release = nil
		}
		m.mu.Unlock()
		return
	}
	job.Status = "preparing"
	if err := m.persistJobLocked(job); err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		if job.release != nil {
			job.release()
			job.release = nil
		}
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	tmp, err := os.CreateTemp(m.root, ".archive-"+job.ID+"-*.wza")
	fileCount := job.Files
	zipBytes := int64(0)
	if err == nil {
		_ = tmp.Chmod(0o600)
		fileCount, zipBytes, err = m.lib.WriteEncryptedArchive(ctx, job.ID, job.manifest, tmp)
		if err == nil {
			err = tmp.Sync()
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), job.path)
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job.Files = fileCount
	job.zipBytes = zipBytes
	if errors.Is(ctx.Err(), context.Canceled) {
		job.Status = "cancelled"
		job.Error = "archive cancelled"
		logArchive(job)
		_ = os.Remove(job.path)
		if job.release != nil {
			job.release()
			job.release = nil
		}
		return
	}
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		logArchive(job)
		if checkpointErr := m.persistJobLocked(job); checkpointErr != nil {
			job.Error = "archive failure checkpoint failed: " + checkpointErr.Error()
		}
		if job.release != nil {
			job.release()
			job.release = nil
		}
		return
	}
	job.Status = "ready"
	job.ExpiresAt = time.Now().UTC().Add(archiveLifetime)
	if err := m.persistJobLocked(job); err != nil {
		job.Status = "failed"
		job.Error = "archive ready checkpoint failed"
		_ = os.Remove(job.path)
	}
	if job.release != nil {
		job.release()
		job.release = nil
	}
	logArchive(job)
}
