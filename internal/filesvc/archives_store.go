package filesvc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type archiveDiskJob struct {
	Version  int                     `json:"version"`
	View     ArchiveJobView          `json:"view"`
	Manifest library.ArchiveManifest `json:"manifest"`
	ZIPBytes int64                   `json:"zip_bytes"`
}

func archiveReservationBytes(manifest library.ArchiveManifest) (int64, error) {
	bytes := manifest.Bytes
	if bytes < 0 {
		return 0, errors.New("archive size is invalid")
	}
	for _, entry := range manifest.Entries {
		extra := int64(len(entry.Path))*4 + 2048
		if bytes > math.MaxInt64-extra {
			return 0, errors.New("archive size overflow")
		}
		bytes += extra
	}
	chunks := bytes/(1<<20) + 1
	if bytes > math.MaxInt64-chunks*28-4 {
		return 0, errors.New("archive size overflow")
	}
	return bytes + chunks*28 + 4, nil
}

func (m *ArchiveManager) persistJobLocked(job *archiveJob) error {
	plain, err := json.Marshal(archiveDiskJob{Version: 1, View: job.ArchiveJobView, Manifest: job.manifest, ZIPBytes: job.zipBytes})
	if err != nil {
		return err
	}
	defer clear(plain)
	if len(plain) > 64<<20 {
		return errors.New("archive checkpoint exceeds its size limit")
	}
	raw, err := m.lib.SealArchiveMetadata(plain)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(filepath.Join(m.root, job.ID+".enc"), raw, 0600)
}

func (m *ArchiveManager) loadJobsLocked() error {
	if m.jobsLoaded {
		return nil
	}
	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		m.jobsLoaded = true
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".enc")
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 16 || entry.Name() != id+".enc" {
			continue
		}
		if m.jobs[id] != nil {
			continue
		}
		disk, err := m.readJob(id)
		if err != nil {
			if errors.Is(err, vault.ErrLocked) {
				return err
			}
			log.Printf("archive checkpoint unavailable id=%s: %v", id, err)
			continue
		}
		if !time.Now().Before(disk.View.ExpiresAt) {
			_ = os.Remove(filepath.Join(m.root, id+".wza"))
			_ = os.Remove(filepath.Join(m.root, id+".enc"))
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		job := &archiveJob{ArchiveJobView: disk.View, manifest: disk.Manifest, zipBytes: disk.ZIPBytes, cancel: cancel, path: filepath.Join(m.root, id+".wza"), done: make(chan struct{}), activityRelease: func() {}}
		if job.Status == "ready" {
			reader, err := m.lib.OpenEncryptedArchive(ctx, id, job.path, job.zipBytes)
			if err != nil {
				job.Status = "failed"
				job.Error = "archive output missing or incomplete"
			} else {
				reader.Close()
			}
			close(job.done)
		} else if job.Status == "queued" || job.Status == "preparing" {
			manifest, err := m.lib.HoldArchiveManifest(ctx, disk.Manifest)
			if err != nil {
				job.Status = "failed"
				job.Error = "archive source unavailable"
				close(job.done)
			} else {
				bytes, err := archiveReservationBytes(manifest)
				if err == nil {
					job.release, err = m.reserveBytes(bytes)
				}
				if err != nil {
					manifest.Release()
					job.Status = "failed"
					job.Error = "archive reservation unavailable"
					close(job.done)
				} else {
					job.manifest = manifest
					job.Status = "queued"
					if m.activity != nil {
						job.activityRelease = m.activity()
					}
					m.workers.Add(1)
					go m.run(ctx, job)
				}
			}
		} else {
			close(job.done)
		}
		m.jobs[id] = job
	}
	m.jobsLoaded = true
	return nil
}

func (m *ArchiveManager) readJob(id string) (archiveDiskJob, error) {
	f, err := os.Open(filepath.Join(m.root, id+".enc"))
	if err != nil {
		return archiveDiskJob{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 96<<20))
	if err != nil || len(raw) >= 96<<20 {
		return archiveDiskJob{}, errors.New("archive checkpoint exceeds its limit")
	}
	plain, err := m.lib.OpenArchiveMetadata(raw)
	if err != nil {
		return archiveDiskJob{}, err
	}
	defer clear(plain)
	var disk archiveDiskJob
	if json.Unmarshal(plain, &disk) != nil || disk.Version != 1 || disk.View.ID != id || disk.ZIPBytes < 0 || len(disk.Manifest.Entries) > 200000 {
		return disk, errors.New("invalid archive checkpoint")
	}
	return disk, nil
}
