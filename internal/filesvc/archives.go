package filesvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
)

// On-demand ZIPs are retained long enough to resume a transfer without
// leaving plaintext archives on the node for a full day. Grab capsules have
// their own encrypted payload and burn lifecycle in internal/capsule.
const archiveLifetime = 90 * time.Minute

type ArchiveJobView struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type archiveJob struct {
	ArchiveJobView
	manifest library.ArchiveManifest
	cancel   context.CancelFunc
	release  func()
	path     string
}

type ArchiveManager struct {
	lib     *library.Library
	root    string
	reserve func(int64) (func(), error)
	slots   chan struct{}
	mu      sync.Mutex
	jobs    map[string]*archiveJob
}

func NewArchiveManager(lib *library.Library, reserve ...func(int64) (func(), error)) *ArchiveManager {
	m := &ArchiveManager{lib: lib, root: lib.ArchiveDir(), slots: make(chan struct{}, 2), jobs: make(map[string]*archiveJob)}
	if len(reserve) > 0 {
		m.reserve = reserve[0]
	}
	_ = os.MkdirAll(m.root, 0o700)
	m.cleanupStaleTemps()
	m.cleanupLocked(time.Now().UTC())
	return m
}

// reserve holds the shared quota reservation for the generated ZIP until the
// archive expires or is removed. It is optional for callers that do not own a
// node quota manager (such as isolated library tests).
func (m *ArchiveManager) reserveBytes(bytes int64) (func(), error) {
	if m.reserve == nil {
		return func() {}, nil
	}
	return m.reserve(bytes)
}

func (m *ArchiveManager) Start(paths []string) (ArchiveJobView, error) {
	if len(paths) == 0 {
		return ArchiveJobView{}, errors.New("archive selection is empty")
	}
	manifest, err := m.lib.PrepareArchive(context.Background(), paths)
	if err != nil {
		return ArchiveJobView{}, err
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return ArchiveJobView{}, err
	}
	reserveBytes := manifest.Bytes
	if len(manifest.Entries) > 0 {
		reserveBytes += int64(len(manifest.Entries)) * 256
	}
	release, err := m.reserveBytes(reserveBytes)
	if err != nil {
		return ArchiveJobView{}, err
	}
	id, err := archiveID()
	if err != nil {
		release()
		return ArchiveJobView{}, err
	}
	created := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	job := &archiveJob{ArchiveJobView: ArchiveJobView{ID: id, Status: "queued", Files: manifest.Files, Bytes: manifest.Bytes, CreatedAt: created, ExpiresAt: created.Add(archiveLifetime)}, manifest: manifest, cancel: cancel, release: release, path: filepath.Join(m.root, id+".zip")}
	m.mu.Lock()
	m.cleanupLocked(created)
	m.jobs[id] = job
	m.mu.Unlock()
	go m.run(ctx, job)
	return job.ArchiveJobView, nil
}

func (m *ArchiveManager) run(ctx context.Context, job *archiveJob) {
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		m.mu.Lock()
		job.Status = "cancelled"
		job.Error = "archive cancelled"
		if job.release != nil {
			job.release()
			job.release = nil
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	job.Status = "preparing"
	m.mu.Unlock()
	tmp, err := os.CreateTemp(m.root, ".archive-*.zip")
	fileCount := job.Files
	byteCount := job.Bytes
	if err == nil {
		_ = tmp.Chmod(0o600)
		fileCount, byteCount, err = m.lib.WriteArchive(ctx, job.manifest, tmp)
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
	job.Bytes = byteCount
	if errors.Is(ctx.Err(), context.Canceled) {
		job.Status = "cancelled"
		job.Error = "archive cancelled"
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
		if job.release != nil {
			job.release()
			job.release = nil
		}
		return
	}
	job.Status = "ready"
}

func (m *ArchiveManager) Get(id string) (ArchiveJobView, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now().UTC())
	job := m.jobs[id]
	if job == nil {
		return ArchiveJobView{}, "", false
	}
	return job.ArchiveJobView, job.path, true
}

func (m *ArchiveManager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.Status == "ready" || job.Status == "failed" || job.Status == "cancelled" {
		return false
	}
	job.cancel()
	job.Status = "cancelled"
	return true
}

// Lock cancels active jobs and removes completed plaintext archives before a
// vault is locked. A new authenticated session can create a fresh archive.
func (m *ArchiveManager) Lock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		job.cancel()
		_ = os.Remove(job.path)
		if job.release != nil {
			job.release()
			job.release = nil
		}
		delete(m.jobs, id)
	}
}

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
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".archive-") {
			continue
		}
		if filepath.Ext(entry.Name()) != ".zip" {
			continue
		}
		if _, ok := m.jobs[entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]]; !ok {
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
