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
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type Library struct {
	mu            sync.Mutex
	stageMu       sync.Mutex
	activeStages  map[string]struct{}
	resticCommits atomic.Uint64
	batchCommits  atomic.Uint64
	batchMu       sync.Mutex
	batchPending  []batchRequest
	batchWake     chan struct{}
	batchRunning  bool
	batchDone     chan struct{}
	thumbMu       sync.Mutex
	thumbJobs     map[string]*thumbnailJob
	changeMu      sync.RWMutex
	changeSink    ChangeSink
	activityMu    sync.RWMutex
	activity      func() func()
	repo          string
	vault         *vault.Vault
	catalog       *catalog.Catalog
	backend       Backend
	sharedStore   *sharedstore.Store
	ownerID       string
	sharedWrites  bool
}

const TrashLifetime = 30 * 24 * time.Hour

func (l *Library) SetChangeSink(sink ChangeSink) {
	l.changeMu.Lock()
	l.changeSink = sink
	l.changeMu.Unlock()
}

func (l *Library) publishChange(change Change) {
	l.changeMu.RLock()
	sink := l.changeSink
	l.changeMu.RUnlock()
	if sink != nil {
		sink.Publish(change)
	}
}

func New(repo, catalogPath string, v *vault.Vault) *Library {
	return &Library{
		repo:         repo,
		vault:        v,
		catalog:      catalog.New(catalogPath, v),
		backend:      newResticBackend(repo, v),
		activeStages: make(map[string]struct{}),
		thumbJobs:    make(map[string]*thumbnailJob),
		batchWake:    make(chan struct{}, 1),
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

func (l *Library) ensure(ctx context.Context) error {
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.backend.Ensure(ctx); err != nil {
		return err
	}
	return l.catalog.Load()
}

func (l *Library) Mkdir(ctx context.Context, name string) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.catalog.Mkdir(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "mkdir", Paths: []string{name}})
	return nil
}

func (l *Library) Rename(ctx context.Context, oldName, newName string) error {
	oldName, err := cleanPath(oldName)
	if err != nil {
		return err
	}
	newName, err = cleanPath(newName)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.catalog.Rename(oldName, newName); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "rename", Paths: []string{oldName, newName}})
	return nil
}

func (l *Library) Copy(ctx context.Context, oldName, newName string) error {
	oldName, err := cleanPath(oldName)
	if err != nil {
		return err
	}
	newName, err = cleanPath(newName)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	err = l.catalog.CopyWith(oldName, newName, func(source, destination *catalog.File) error {
		if source.Reference == nil || source.Reference.Backend != catalog.SharedBackend {
			return nil
		}
		if l.sharedStore == nil || l.ownerID == "" {
			return catalog.ErrUnknownReference
		}
		ref, e := l.sharedStore.Grant(ctx, l.ownerID, l.vault, toSharedReference(*source.Reference), destination.EntryID, destination.Revision)
		if e != nil {
			return e
		}
		destination.Reference = &catalog.Reference{Backend: catalog.SharedBackend, Version: 1, Object: ref.ObjectID, Operation: ref.Operation, OwnerEntryID: ref.EntryID, OwnerRevision: ref.Revision}
		destination.Object = ref.ObjectID
		return nil
	})
	if err != nil {
		return err
	}
	l.publishChange(Change{Kind: "copy", Paths: []string{oldName, newName}})
	return nil
}

func (l *Library) Put(ctx context.Context, name string, body []byte) (catalog.File, error) {
	return l.PutReader(ctx, name, bytes.NewReader(body), int64(len(body)))
}

func (l *Library) PutReader(ctx context.Context, name string, body io.Reader, expected int64) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	stage, err := l.stageReader(name, body, expected)
	if err != nil {
		return catalog.File{}, err
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
	if err := l.catalog.Load(); err != nil {
		return err
	}
	if err := l.catalog.Delete(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "delete", Paths: []string{name}})
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

// StreamRange reads a byte range without retaining the complete file.
func (l *Library) StreamRange(ctx context.Context, name string, offset, length int64, w io.Writer) error {
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
	if !ok || f.Folder || offset < 0 || length < 0 || offset > f.Size || length > f.Size-offset {
		return errors.New("invalid file range")
	}
	ref, err := l.capture(f)
	if err != nil {
		return err
	}
	return l.readReferenceRange(ctx, ref, offset, length, w)
}

func (l *Library) ResticCommitCounts() (single, batch uint64) {
	return l.resticCommits.Load(), l.batchCommits.Load()
}

func (l *Library) ArchiveDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-archives")
}
