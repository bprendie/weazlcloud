package library

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type grantOrderBackend struct {
	isolatedLegacy
	inGrant *atomic.Bool
}

func (b *grantOrderBackend) Ensure(ctx context.Context) error {
	if b.inGrant.Load() {
		return errors.New("storage initialization under users grant lock")
	}
	return b.isolatedLegacy.Ensure(ctx)
}

// Photo and backup must acquire library -> users -> catalog, and initialize
// storage before taking users.mu. Invalid photo specs still exercise publication
// admission, without putting physical storage or quota work under the grant lock.
func TestConcurrentPhotoAndBackupGrantPublicationLockOrder(t *testing.T) {
	root := t.TempDir()
	us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := us.CreateScopedDevice(u.ID, "phone", []string{users.PhotosWrite, users.FilesWrite, users.BackupWrite})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/photos/uploads", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	grant, err := us.GrantForRequest(req, users.PhotosWrite, users.FilesWrite, users.BackupWrite)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(us.VaultPath(u), us.NodeKeyPath(u))
	if err := v.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	defer v.Lock()
	l := New(filepath.Join(root, "repo"), filepath.Join(root, "catalog.enc"), v)
	var inGrant atomic.Bool
	l.backend = &grantOrderBackend{isolatedLegacy: isolatedLegacy{root: filepath.Join(root, "repo")}, inGrant: &inGrant}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	guard := func(publish func() error) error {
		if l.mu.TryLock() {
			l.mu.Unlock()
			return errors.New("grant acquired before library mutation lock")
		}
		return us.WithDeviceGrant(grant, func() error {
			inGrant.Store(true)
			defer inGrant.Store(false)
			return publish()
		}, users.PhotosWrite)
	}
	// Check ordering deterministically without another thread masking TryLock.
	if _, err := l.CommitPhotoIngestGuarded(ctx, catalog.PhotoIngestCommit{}, guard); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("photo grant lock order", err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		for range 40 {
			if _, err := l.CommitPhotoIngestGuarded(ctx, catalog.PhotoIngestCommit{}, guard); !errors.Is(err, catalog.ErrConflict) {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		<-start
		for i := range 40 {
			if err := l.WithBackup(ctx, func(tx *BackupTransaction) error {
				tx.SetCommitGuard(guard)
				return tx.WriteAuthorized(strings.Repeat("a", 32), map[string]int{"revision": i})
			}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent photo/backup publication deadlocked")
		}
	}
	if err := us.RevokeDevice(u.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CommitPhotoIngestGuarded(ctx, catalog.PhotoIngestCommit{}, guard); !errors.Is(err, users.ErrNoSession) {
		t.Fatal("revoked photo published", err)
	}
	if err := l.WithBackup(ctx, func(tx *BackupTransaction) error {
		tx.SetCommitGuard(guard)
		return tx.WriteAuthorized(strings.Repeat("a", 32), map[string]int{"revision": 99})
	}); !errors.Is(err, users.ErrNoSession) {
		t.Fatal("revoked backup wrote private intent", err)
	}
}
