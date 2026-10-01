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
// retaining encrypted archives on the node for a full day. Grab capsules have
// their own encrypted payload and burn lifecycle in internal/capsule.
const archiveLifetime = 90 * time.Minute

type ArchiveJobView struct {
	Scope     string    `json:"scope,omitempty"`
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type archiveJob struct {
	zipBytes int64
	ArchiveJobView
	manifest        library.ArchiveManifest
	cancel          context.CancelFunc
	release         func()
	activityRelease func()
	path            string
	done            chan struct{}
}

type ArchiveManager struct {
	jobsLoaded bool
	lib        *library.Library
	root       string
	reserve    func(int64) (func(), error)
	activity   func() func()
	slots      chan struct{}
	mu         sync.Mutex
	jobs       map[string]*archiveJob
	closing    bool
	workers    sync.WaitGroup
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
	activity := m.trackStorage()
	if len(paths) == 0 {
		activity()
		return ArchiveJobView{}, errors.New("archive selection is empty")
	}
	manifest, err := m.lib.PrepareArchive(context.Background(), paths)
	if err != nil {
		activity()
		return ArchiveJobView{}, err
	}
	return m.startManifest(manifest, activity)
}

// StartManifest accepts an owner-authorized, pinned selection. It owns the
// holds after this call, including failure cleanup.
func (m *ArchiveManager) StartManifest(manifest library.ArchiveManifest) (ArchiveJobView, error) {
	return m.startManifest(manifest, m.trackStorage())
}

func (m *ArchiveManager) startManifest(manifest library.ArchiveManifest, releaseActivity func()) (ArchiveJobView, error) {
	handedOff := false
	defer func() {
		if !handedOff {
			releaseActivity()
		}
	}()
	manifestHandedOff := false
	defer func() {
		if !manifestHandedOff {
			manifest.Release()
		}
	}()
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return ArchiveJobView{}, err
	}
	reserveBytes, err := archiveReservationBytes(manifest)
	if err != nil {
		return ArchiveJobView{}, err
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
	if err := m.loadJobsLocked(); err != nil {
		m.mu.Unlock()
		cancel()
		release()
		return ArchiveJobView{}, err
	}
	if m.closing {
		m.mu.Unlock()
		cancel()
		release()
		return ArchiveJobView{}, errors.New("archive manager is closing")
	}
	job := &archiveJob{ArchiveJobView: ArchiveJobView{ID: id, Status: "queued", Files: manifest.Files, Bytes: manifest.Bytes, CreatedAt: created, ExpiresAt: created.Add(24 * time.Hour)}, manifest: manifest, cancel: cancel, release: release, activityRelease: releaseActivity, path: filepath.Join(m.root, id+".wza"), done: make(chan struct{})}
	if manifest.PhotoOnly() {
		job.Scope = "photos"
	}
	if err := m.persistJobLocked(job); err != nil {
		m.mu.Unlock()
		cancel()
		release()
		return ArchiveJobView{}, err
	}
	m.jobs[id] = job
	m.workers.Add(1)
	view := job.ArchiveJobView
	m.mu.Unlock()
	handedOff = true
	manifestHandedOff = true
	go m.run(ctx, job)
	return view, nil
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
	if err := m.loadJobsLocked(); err != nil {
		return ArchiveJobView{}, "", false
	}
	job := m.jobs[id]
	if job == nil {
		return ArchiveJobView{}, "", false
	}
	if !time.Now().UTC().Before(job.ExpiresAt) {
		job.cancel()
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
	job.Status = "cancelled"
	job.Error = "archive cancelled"
	if err := m.persistJobLocked(job); err != nil {
		job.Status = "failed"
		job.Error = "archive cancellation checkpoint failed"
		logArchive(job)
	}
	job.cancel()
	return true
}

// Lock cancels active jobs and removes completed encrypted archives before a
// vault is locked. A new authenticated session can create a fresh archive.
func (m *ArchiveManager) Lock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		job.cancel()
		_ = os.Remove(job.path)
		_ = os.Remove(filepath.Join(m.root, id+".enc"))
		if job.release != nil {
			job.release()
			job.release = nil
		}
		delete(m.jobs, id)
	}
}
