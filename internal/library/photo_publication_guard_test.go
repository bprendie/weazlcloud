package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/users"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPhotoAndSourcePublicationMatchBackupGrantLockOrder(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.PhotoCollections(ctx, "", nil, 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create("guard-owner", "guard-password", true)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := store.CreateScopedDevice(owner.ID, "phone", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/photos/uploads", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	grant, err := store.GrantForRequest(r, users.PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	guard := func(publish func() error) error {
		// Deterministically catch users-before-library inversion without hanging a test.
		if l.mu.TryLock() {
			l.mu.Unlock()
			return errors.New("grant acquired before library mutation lock")
		}
		return store.WithDeviceGrant(grant, publish, users.PhotosWrite)
	}
	pending := catalog.File{Path: ".weazl-mobile-pending/order/a.jpg", Size: 1, Hash: "hash-a", Snap: "fixture"}
	if err = l.catalog.Put(pending); err != nil {
		t.Fatal(err)
	}
	commit := catalog.PhotoIngestCommit{DeviceID: device.ID, DeviceAssetID: "asset", SourceRevision: "1", Files: []catalog.PhotoIngestFile{{ID: "original", From: pending.Path, To: "Photos/a.jpg", Size: 1, Hash: pending.Hash, MediaType: "image/jpeg"}}}
	ops := []catalog.SourceOperation{{OperationID: "import", Namespace: "test", SourceID: "album", SourceRevision: "1", Kind: "album", Title: "Album"}}
	start := make(chan struct{})
	finished := make(chan error, 3)
	var wg sync.WaitGroup
	for _, work := range []func() error{
		func() error { _, e := l.CommitPhotoIngestGuarded(ctx, commit, guard); return e },
		func() error { _, e := l.ImportPhotoSourcesGuarded(ctx, device.ID, ops, false, guard); return e },
		func() error {
			return l.WithBackup(ctx, func(tx *BackupTransaction) error {
				tx.SetCommitGuard(guard)
				return tx.WriteAuthorized(strings.Repeat("a", 32), map[string]int{"version": 1})
			})
		},
	} {
		wg.Add(1)
		go func(work func() error) { defer wg.Done(); <-start; finished <- work() }(work)
	}
	close(start)
	for i := 0; i < 3; i++ {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("publication deadlocked")
		}
	}
	wg.Wait()
	if err = store.RevokeDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	pending.Path = ".weazl-mobile-pending/order/b.jpg"
	if err = l.catalog.Put(pending); err != nil {
		t.Fatal(err)
	}
	commit.DeviceAssetID = "second"
	commit.Files[0].From = pending.Path
	commit.Files[0].To = "Photos/b.jpg"
	if _, err = l.CommitPhotoIngestGuarded(ctx, commit, guard); !errors.Is(err, users.ErrNoSession) {
		t.Fatalf("revoked photo publish: %v", err)
	}
	if _, ok := l.catalog.Get("Photos/b.jpg"); ok {
		t.Fatal("revoked photo published")
	}
	if _, ok := l.catalog.Get(pending.Path); !ok {
		t.Fatal("revocation lost pending component")
	}
	ops[0].OperationID, ops[0].SourceID = "denied", "new-album"
	if _, err = l.ImportPhotoSourcesGuarded(ctx, device.ID, ops, false, guard); !errors.Is(err, users.ErrNoSession) {
		t.Fatalf("revoked source publish: %v", err)
	}
	if len(l.catalog.Albums()) != 1 {
		t.Fatal("revoked source published")
	}
}
