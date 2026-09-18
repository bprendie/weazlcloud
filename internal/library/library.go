package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type Library struct {
	mu      sync.Mutex
	repo    string
	vault   *vault.Vault
	catalog *catalog.Catalog
	restic  restic.Runner
}

func New(repo, catalogPath string, v *vault.Vault) *Library {
	return &Library{
		repo:    repo,
		vault:   v,
		catalog: catalog.New(catalogPath, v),
		restic:  restic.New(),
	}
}

func (l *Library) Ensure(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ensure(ctx)
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

func (l *Library) Put(ctx context.Context, name string, body []byte) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return catalog.File{}, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	snap, err := l.restic.Put(ctx, restic.Repo{Location: l.repo, Password: pass}, hash, bytes.NewReader(body))
	if err != nil {
		return catalog.File{}, err
	}
	f := catalog.File{
		Path: name, Size: int64(len(body)), Mtime: time.Now().UTC(),
		Hash: hash, Snap: snap, Present: true,
	}
	if err := l.catalog.Put(f); err != nil {
		return catalog.File{}, err
	}
	return f, nil
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
	if err := l.restic.Dump(ctx, restic.Repo{Location: l.repo, Password: pass}, f.Snap, f.Hash, &buf); err != nil {
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
	return l.catalog.Delete(name)
}

func (l *Library) WriteTo(ctx context.Context, name string, w io.Writer) error {
	b, err := l.Get(ctx, name)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
