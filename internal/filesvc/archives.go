package filesvc

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
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
	manifest        library.ArchiveManifest
	cancel          context.CancelFunc
	release         func()
	activityRelease func()
	path            string
	done            chan struct{}
}

type ArchiveManager struct {
	lib      *library.Library
	root     string
	reserve  func(int64) (func(), error)
	activity func() func()
	slots    chan struct{}
	mu       sync.Mutex
	jobs     map[string]*archiveJob
	closing  bool
	workers  sync.WaitGroup
}

func (m *ArchiveManager) SetActivityTracker(track func() func()) {
	m.mu.Lock()
	m.activity = track
	m.mu.Unlock()
}

func (m *ArchiveManager) trackStorage() func() {
	m.mu.Lock()
	track := m.activity
	m.mu.Unlock()
	if track == nil {
		return func() {}
	}
	return track()
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
	releaseActivity := m.trackStorage()
	handedOff := false
	defer func() {
		if !handedOff {
			releaseActivity()
		}
	}()
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
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		cancel()
		release()
		return ArchiveJobView{}, errors.New("archive manager is closing")
	}
	job := &archiveJob{ArchiveJobView: ArchiveJobView{ID: id, Status: "queued", Files: manifest.Files, Bytes: manifest.Bytes, CreatedAt: created, ExpiresAt: created.Add(archiveLifetime)}, manifest: manifest, cancel: cancel, release: release, activityRelease: releaseActivity, path: filepath.Join(m.root, id+".zip"), done: make(chan struct{})}
	m.cleanupLocked(created)
	m.jobs[id] = job
	m.workers.Add(1)
	m.mu.Unlock()
	handedOff = true
	go m.run(ctx, job)
	return job.ArchiveJobView, nil
}

func (m *ArchiveManager) run(ctx context.Context, job *archiveJob) {
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
		if job.release != nil {
			job.release()
			job.release = nil
		}
		return
	}
	job.Status = "ready"
	logArchive(job)
}

func (m *ArchiveManager) Drain(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	for _, job := range m.jobs {
		job.cancel()
	}
	m.mu.Unlock()
	wait := make(chan struct{})
	go func() { m.workers.Wait(); close(wait) }()
	select {
	case <-wait:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.Lock()
	_ = os.RemoveAll(m.root)
	return nil
}

func logArchive(job *archiveJob) {
	log.Printf("archive status=%s files=%d bytes=%d duration_ms=%d", job.Status, job.Files, job.Bytes, time.Since(job.CreatedAt).Milliseconds())
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
