package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type Library struct {
	metadataMu                 sync.Mutex
	metadataJob                *PhotoMetadataJob
	metadataRunning            bool
	metadataCancel             context.CancelFunc
	metadataDone               chan struct{}
	mu                         sync.Mutex
	stageMu                    sync.Mutex
	activeStages               map[string]struct{}
	resticCommits              atomic.Uint64
	batchCommits               atomic.Uint64
	batchMu                    sync.Mutex
	batchPending               []batchRequest
	batchWake                  chan struct{}
	batchRunning               bool
	batchDone                  chan struct{}
	thumbMu                    sync.Mutex
	thumbJobs                  map[string]*thumbnailJob
	changeMu                   sync.RWMutex
	changeSink                 ChangeSink
	activityMu                 sync.RWMutex
	activity                   func() func()
	previewLease               func(context.Context) (context.Context, func(), bool)
	previewLifetime            context.Context
	stopPreviews               context.CancelFunc
	repo                       string
	vault                      *vault.Vault
	catalog                    *catalog.Catalog
	catalogSession             uint64
	catalogLoaded              bool
	storageSummary             StorageSummary
	storageSummaryAt           time.Time
	storageSummaryVersion      uint64
	storageSummaryReady        bool
	backend                    Backend
	sharedStore                *sharedstore.Store
	ownerID                    string
	sharedWrites               bool
	albumMetadata              map[string]albumMetadata
	photoMu                    sync.Mutex
	photoRows                  []catalog.File
	photoMediaRows             []catalog.File
	photoByID                  map[string]catalog.File
	photoByPath                map[string]int
	photoMediaByPath           map[string]int
	photoMediaByID             map[string]int
	photoHiddenFolders         map[string]bool
	photoArchivedCount         int
	photoReady                 bool
	photoEpoch                 uint64
	photoSortedEpoch           uint64
	photoQueryCache            map[string]photoQueryView
	photoQueryOrder            []string
	photoQueryEpoch            uint64
	photoNavigationCache       map[string]*photoNavigationProjection
	photoNavigationEpoch       uint64
	photoDateSummary           PhotoDateSummary
	photoDateSummaryEpoch      uint64
	photoSave                  *time.Timer
	photoSaveEpoch             uint64
	photoSaveWG                sync.WaitGroup
	photoPrepMu                sync.Mutex
	photoPrep                  photoPreparation
	photoPrepLoaded            bool
	photoPrepRunning           bool
	photoResumeWaiting         bool
	photoPrepCancel            context.CancelFunc
	photoStoppingForLock       bool
	photoJobsMu                sync.Mutex
	photoJobs                  photos.JobQueue
	photoJobsLoaded            bool
	photoPrepared              map[string]int
	photoCacheEpoch            uint64
	photoImports               int
	pendingPhotoIngest         map[string]catalog.File
	pendingMetadataDirectories map[string]bool
	photoAutoDisabled          bool
	photoFailureMu             sync.Mutex
	photoFailureCount          int
	photoFailuresKnown         bool
}

const TrashLifetime = 30 * 24 * time.Hour

func New(repo, catalogPath string, v *vault.Vault) *Library {
	logPreviewPolicy()
	previewLifetime, stopPreviews := context.WithCancel(context.Background())
	return &Library{
		previewLifetime: previewLifetime,
		stopPreviews:    stopPreviews,
		repo:            repo,
		vault:           v,
		catalog:         catalog.New(catalogPath, v),
		backend:         newResticBackend(repo, v),
		activeStages:    make(map[string]struct{}),
		thumbJobs:       make(map[string]*thumbnailJob),
		batchWake:       make(chan struct{}, 1),
	}
}

func (l *Library) Ensure(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	return l.recoverStaged(ctx)
}

func (l *Library) Put(ctx context.Context, name string, body []byte) (catalog.File, error) {
	return l.PutReader(ctx, name, bytes.NewReader(body), int64(len(body)))
}

func (l *Library) PutReader(ctx context.Context, name string, body io.Reader, expected int64) (catalog.File, error) {
	return l.PutReaderAt(ctx, name, body, expected, time.Time{})
}

// PutReaderAt preserves source timestamps for trusted server-side imports.
func (l *Library) PutReaderAt(ctx context.Context, name string, body io.Reader, expected int64, mtime time.Time) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	stage, err := l.stageReader(name, body, expected)
	if err != nil {
		return catalog.File{}, err
	}
	if !mtime.IsZero() {
		stage.Mtime = mtime.UTC()
		if err := l.writeStage(stage); err != nil {
			return catalog.File{}, err
		}
	}
	return l.commitStagedQueued(ctx, stage)
}

func (l *Library) Get(ctx context.Context, name string) ([]byte, error) {
	name, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	f, ok := l.catalog.Get(name)
	if !ok {
		return nil, errors.New("file is not in the library")
	}
	ref, err := l.capture(f)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := l.readReference(ctx, ref, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (l *Library) Delete(name string) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	_, session := l.vault.State()
	if err := l.loadCatalogSession(session); err != nil {
		return err
	}
	paths := l.catalog.RelatedPhotoPaths(name, false)
	if err := l.catalog.Delete(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "delete", Paths: paths})
	return nil
}

func (l *Library) WriteTo(ctx context.Context, name string, w io.Writer) error {
	return l.StreamTo(ctx, name, w)
}

// StreamTo restores a file directly into w. The caller controls buffering;
// this keeps large downloads and WebDAV reads out of process memory.
func (l *Library) StreamTo(ctx context.Context, name string, w io.Writer) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	f, ok := l.catalog.Get(name)
	if !ok {
		return errors.New("file is not in the library")
	}
	ref, err := l.capture(f)
	if err != nil {
		return err
	}
	return l.readReference(ctx, ref, w)
}

func (l *Library) ResticCommitCounts() (single, batch uint64) {
	return l.resticCommits.Load(), l.batchCommits.Load()
}

func (l *Library) ArchiveDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-archives")
}
