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
	"github.com/bprendie/weazlcloud/internal/restic"
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
	thumbMu       sync.Mutex
	thumbJobs     map[string]*thumbnailJob
	changeMu      sync.RWMutex
	changeSink    ChangeSink
	repo          string
	vault         *vault.Vault
	catalog       *catalog.Catalog
	restic        restic.Runner
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
		restic:       restic.New(),
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
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return err
	}
	if err := l.restic.Init(ctx, restic.Repo{Location: l.repo, Password: pass}); err != nil {
		return err
	}
	return l.catalog.Load()
}

func (l *Library) List() []catalog.File {
	return l.catalog.List()
}

func (l *Library) Usage(ctx context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, err
	}
	var total int64
	for _, f := range l.catalog.List() {
		total += f.Size
	}
	return total, nil
}

func (l *Library) Metadata(ctx context.Context, name string) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	f, ok := l.catalog.Get(name)
	if !ok {
		return catalog.File{}, errors.New("file is not in the library")
	}
	return f, nil
}

func (l *Library) Dedupe(ctx context.Context) (int, int64, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, 0, 0, err
	}
	logical := int64(0)
	unique := int64(0)
	hashes := make(map[string]int64)
	for _, f := range l.catalog.List() {
		if f.Folder {
			continue
		}
		logical += f.Size
		if _, ok := hashes[f.Hash]; !ok {
			hashes[f.Hash] = f.Size
			unique += f.Size
		}
	}
	percent := 0
	if logical > 0 {
		percent = int((logical - unique) * 100 / logical)
	}
	return percent, logical, unique, nil
}

func (l *Library) Trash(ctx context.Context) ([]catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	return l.catalog.Trash(), nil
}

func (l *Library) Restore(ctx context.Context, name string) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.catalog.Restore(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "restore", Paths: []string{name}})
	return nil
}

func (l *Library) CleanupTrash(ctx context.Context, before time.Time) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, err
	}
	all := l.catalog.All()
	protected := make(map[string]struct{})
	var reclaimable int64
	for _, f := range all {
		if f.Present || f.DeletedAt == nil || f.DeletedAt.After(before) {
			if f.Snap != "" {
				protected[f.Snap] = struct{}{}
			}
		}
		if !f.Present && f.DeletedAt != nil && !f.DeletedAt.After(before) && !f.Folder {
			reclaimable += f.Size
		}
	}
	if reclaimable == 0 {
		return 0, nil
	}
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return 0, err
	}
	snapshots, err := l.restic.Snapshots(ctx, restic.Repo{Location: l.repo, Password: pass})
	if err != nil {
		return 0, err
	}
	forget := make([]string, 0)
	for _, snap := range snapshots {
		if _, ok := protected[snap]; !ok {
			forget = append(forget, snap)
		}
	}
	removed, err := l.catalog.PurgeTrash(before)
	if err != nil {
		return 0, err
	}
	if err := l.restic.Forget(ctx, restic.Repo{Location: l.repo, Password: pass}, forget); err != nil {
		return 0, err
	}
	l.publishChange(Change{Kind: "trash-purge"})
	var removedBytes int64
	for _, f := range removed {
		if !f.Folder {
			removedBytes += f.Size
		}
	}
	return removedBytes, nil
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
	if err := l.catalog.Copy(oldName, newName); err != nil {
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
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	object := f.Object
	if object == "" {
		object = f.Hash
	}
	if err := l.restic.Dump(ctx, restic.Repo{Location: l.repo, Password: pass}, f.Snap, object, &buf); err != nil {
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
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return err
	}
	object := f.Object
	if object == "" {
		object = f.Hash
	}
	return l.restic.Dump(ctx, restic.Repo{Location: l.repo, Password: pass}, f.Snap, object, w)
}

func (l *Library) ResticCommitCounts() (single, batch uint64) {
	return l.resticCommits.Load(), l.batchCommits.Load()
}

func (l *Library) ArchiveDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-archives")
}
