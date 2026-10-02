package library

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// BackupTransaction holds the same mutation lock as browser/WebDAV operations.
// Callers must hold a Registry.Enter owner lease, including background workers.
// No decrypted registry or receipt is retained across calls or vault locks.
type BackupTransaction struct {
	lib         *Library
	ctx         context.Context
	commitGuard func(func() error) error
}

func (l *Library) WithBackup(ctx context.Context, fn func(*BackupTransaction) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return err
	}
	return fn(&BackupTransaction{lib: l, ctx: ctx})
}

func (t *BackupTransaction) SetCommitGuard(guard func(func() error) error) { t.commitGuard = guard }

func (t *BackupTransaction) Catalog() *catalog.Catalog { return t.lib.catalog }
func (t *BackupTransaction) OwnerID() string           { return t.lib.ownerID }
func (t *BackupTransaction) Key(domain string, value any) (string, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	defer cryptox.Zero(plain)
	sum, err := t.lib.vault.Fingerprint("backup:"+domain, plain)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:16]), nil
}
func (t *BackupTransaction) dir() string {
	return filepath.Join(filepath.Dir(t.lib.repo), ".weazl-backups")
}
func backupKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}
func (t *BackupTransaction) Read(key string, dst any) error {
	if !backupKey(key) {
		return catalog.ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(t.dir(), key+".enc"))
	if err != nil {
		return err
	}
	plain, err := t.lib.vault.Unwrap(raw)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	return json.Unmarshal(plain, dst)
}
func (t *BackupTransaction) Write(key string, value any) error {
	if !backupKey(key) {
		return catalog.ErrNotFound
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	raw, err := t.lib.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.dir(), 0700); err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(filepath.Join(t.dir(), key+".enc"), raw, 0600); err != nil {
		return err
	}
	dir, err := os.Open(t.dir())
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (t *BackupTransaction) Keys() ([]string, error) {
	entries, err := os.ReadDir(t.dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".enc") {
			key := strings.TrimSuffix(e.Name(), ".enc")
			if backupKey(key) {
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}
func (t *BackupTransaction) Publish(plan catalog.BackupMutation) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if !t.lib.vault.Unlocked() {
		return vault.ErrLocked
	}
	publish := func() error { return t.lib.catalog.PublishBackup(plan) }
	var err error
	if t.commitGuard != nil {
		err = t.commitGuard(publish)
	} else {
		err = publish()
	}
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(plan.Files)+len(plan.Guards))
	for _, f := range plan.Guards {
		paths = append(paths, f.Path)
	}
	for _, f := range plan.Files {
		paths = append(paths, f.Path)
	}
	t.lib.publishChange(Change{Kind: "put", Paths: paths})
	return nil
}

// WriteAuthorized checks request/device validity atomically with private source
// or intent creation, using the same supplied guard as catalog publication.
func (t *BackupTransaction) WriteAuthorized(key string, value any) error {
	write := func() error { return t.Write(key, value) }
	if t.commitGuard != nil {
		return t.commitGuard(write)
	}
	return write()
}
