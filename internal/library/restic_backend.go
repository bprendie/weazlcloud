package library

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type resticBackend struct {
	path    string
	vault   *vault.Vault
	runner  restic.Runner
	mu      sync.Mutex
	holds   map[string]int
	pruning map[string]bool
	changed chan struct{}
}

func newResticBackend(path string, v *vault.Vault) *resticBackend {
	return &resticBackend{path: path, vault: v, runner: restic.New(), holds: make(map[string]int), pruning: make(map[string]bool), changed: make(chan struct{})}
}

func (b *resticBackend) Ensure(ctx context.Context) error {
	return b.withRepo(ctx, func(repo restic.Repo) error { return b.runner.Init(ctx, repo) })
}

func (b *resticBackend) Put(ctx context.Context, name string, body io.Reader) (catalog.Reference, error) {
	var ref catalog.Reference
	err := b.withRepo(ctx, func(repo restic.Repo) error {
		snapshot, err := b.runner.Put(ctx, repo, name, body)
		if err == nil {
			ref = resticReference(snapshot, name, "")
		}
		return err
	})
	return ref, err
}

func (b *resticBackend) PutBatch(ctx context.Context, root string) (BatchReference, error) {
	var result BatchReference
	err := b.withRepo(ctx, func(repo restic.Repo) error {
		snapshot, err := b.runner.PutBatch(ctx, repo, root)
		result.Snapshot = snapshot
		return err
	})
	return result, err
}

func (b *resticBackend) Read(ctx context.Context, ref catalog.Reference, output io.Writer) error {
	if err := validateResticReference(ref); err != nil {
		return err
	}
	return b.withRepo(ctx, func(repo restic.Repo) error {
		return b.runner.Dump(ctx, repo, ref.Snapshot, ref.Object, output)
	})
}

func (b *resticBackend) ReadRange(ctx context.Context, ref catalog.Reference, offset, length int64, output io.Writer) error {
	if offset < 0 || length < 0 {
		return errors.New("invalid storage range")
	}
	if length == 0 {
		return nil
	}
	rangeOut := &rangeWriter{offset: offset, length: length, output: output}
	if err := b.Read(ctx, ref, rangeOut); err != nil {
		return err
	}
	if rangeOut.written != length {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (b *resticBackend) Capture(file catalog.File) (catalog.Reference, error) {
	ref, err := fileReference(file)
	if err != nil {
		return catalog.Reference{}, err
	}
	if err := validateResticReference(ref); err != nil {
		return catalog.Reference{}, err
	}
	return ref, nil
}

func (b *resticBackend) Snapshots(ctx context.Context) ([]string, error) {
	var snapshots []string
	err := b.withRepo(ctx, func(repo restic.Repo) error {
		var err error
		snapshots, err = b.runner.Snapshots(ctx, repo)
		return err
	})
	return snapshots, err
}

func (b *resticBackend) Forget(ctx context.Context, snapshots []string) ([]string, error) {
	b.mu.Lock()
	ready := make([]string, 0, len(snapshots))
	deferred := make([]string, 0)
	for _, id := range snapshots {
		if b.holds[id] > 0 || b.pruning[id] {
			deferred = append(deferred, id)
		} else {
			ready = append(ready, id)
			b.pruning[id] = true
		}
	}
	b.mu.Unlock()
	if len(ready) == 0 {
		return deferred, nil
	}
	err := b.withRepo(ctx, func(repo restic.Repo) error { return b.runner.Forget(ctx, repo, ready) })
	b.mu.Lock()
	for _, id := range ready {
		delete(b.pruning, id)
	}
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
	return deferred, err
}

func (b *resticBackend) Hold(ref catalog.Reference) (func(), error) {
	if err := validateResticReference(ref); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if b.pruning[ref.Snapshot] {
		b.mu.Unlock()
		return nil, errors.New("storage reference is being pruned")
	}
	b.holds[ref.Snapshot]++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.holds[ref.Snapshot]--
			if b.holds[ref.Snapshot] == 0 {
				delete(b.holds, ref.Snapshot)
			}
			close(b.changed)
			b.changed = make(chan struct{})
			b.mu.Unlock()
		})
	}, nil
}

func (b *resticBackend) Drain(ctx context.Context) error {
	for {
		b.mu.Lock()
		if len(b.holds) == 0 && len(b.pruning) == 0 {
			b.mu.Unlock()
			return nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *resticBackend) withRepo(ctx context.Context, run func(restic.Repo) error) error {
	password, _, err := b.vault.Secrets()
	if err != nil {
		return err
	}
	defer cryptox.Zero(password)
	return run(restic.Repo{Location: b.path, Password: password})
}

func validateResticReference(ref catalog.Reference) error {
	if ref.Backend != catalog.ResticBackend || ref.Version != 1 || ref.Snapshot == "" || ref.Object == "" {
		return catalog.ErrUnknownReference
	}
	return nil
}

type rangeWriter struct {
	offset  int64
	length  int64
	written int64
	output  io.Writer
}

func (w *rangeWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.offset > 0 {
		drop := min(int64(len(p)), w.offset)
		p = p[drop:]
		w.offset -= drop
	}
	if len(p) > 0 && w.written < w.length {
		count := min(int64(len(p)), w.length-w.written)
		written, err := w.output.Write(p[:count])
		w.written += int64(written)
		if err != nil {
			return 0, err
		}
		if int64(written) != count {
			return 0, io.ErrShortWrite
		}
	}
	return n, nil
}
