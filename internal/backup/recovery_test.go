package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

func TestRecoverPublicationBeforeReceiptEvenAfterUserEdit(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	spec := fileSpec(src, "one", "r1", "one", []byte("original"))
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	// Stop exactly after catalog publication, before source mapping or receipt.
	err = with(ctx, res, u, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, v.ID, "phone")
		if err != nil {
			return err
		}
		for i, f := range op.Plan.Files {
			if f.EntryID == op.PrimaryID {
				f, err = tx.StoreReader(op.ID, f, bytes.NewReader([]byte("original")))
				if err != nil {
					return err
				}
				op.Plan.Files[i] = f
			}
		}
		op.Status = "publishing"
		if err := tx.Write(op.ID, op); err != nil {
			return err
		}
		src, err := loadSource(tx, op.SourceKey)
		if err != nil {
			return err
		}
		src.Pending = op.ID
		if err := tx.Write(op.SourceKey, src); err != nil {
			return err
		}
		return tx.Publish(op.Plan)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = res.Lib.Put(ctx, "Backups/one", []byte("user edit"))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := res.Lib.Metadata(ctx, "Backups/one")
	if err != nil {
		t.Fatal(err)
	}
	res.Lib = library.New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), res.Vault)
	res.Lib.ConfigureShared(u.ID, nil, false)
	recovered, err := New(nil).FinalizeParts(ctx, res, u, v.ID, "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.File.Revision != 1 || recovered.File.EntryID != edited.EntryID {
		t.Fatalf("not original receipt: %+v", recovered)
	}
	got, err := res.Lib.Get(ctx, "Backups/one")
	if err != nil || string(got) != "user edit" {
		t.Fatalf("recovery overwrote user: %q %v", got, err)
	}
}

func TestReceiptWriteFailureRecoversFromCatalogProof(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	spec := fileSpec(src, "one", "r1", "one", []byte("original"))
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	// Device commit guard also provides a deterministic fault boundary here:
	// publish succeeds, but replace the operation file with a directory so its
	// receipt cannot be written. Restore its publishing journal to simulate repair.
	var journal []byte
	m.SetCommitGuard(func(publish func() error) error {
		if err := publish(); err != nil {
			return err
		}
		p := filepath.Join(dir, ".weazl-backups", v.ID+".enc")
		var err error
		journal, err = os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		return os.Mkdir(p, 0700)
	})
	if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader([]byte("original"))); err == nil {
		t.Fatal("receipt write unexpectedly succeeded")
	}
	p := filepath.Join(dir, ".weazl-backups", v.ID+".enc")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, journal, 0600); err != nil {
		t.Fatal(err)
	}
	m = New(nil)
	recovered, err := m.Status(ctx, res, u, v.ID, "phone")
	if err != nil || recovered.Status != "stored" || recovered.File.Revision != 1 {
		t.Fatalf("receipt recovery: %+v %v", recovered, err)
	}
}

func TestSharedBackupCommitBoundaryAndReplacement(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	failOnce := true
	store, err := sharedstore.Open(filepath.Join(dir, "node"), sharedstore.Options{FailureHook: func(point string) error {
		if point == "published" && failOnce {
			failOnce = false
			return errors.New("injected shared publish failure")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	res.Lib.ConfigureShared(u.ID, store, true)
	body := []byte("shared bytes")
	spec := fileSpec(src, "one", "r1", "one", body)
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader(body)); err == nil {
		t.Fatal("fault not exercised")
	}
	v, err = New(nil).FinalizeParts(ctx, res, u, v.ID, "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.File.Reference.Backend != catalog.SharedBackend || v.File.Reference.Operation != v.ID {
		t.Fatalf("missing storage operation binding: %+v", v.File)
	}
	got, err := res.Lib.Get(ctx, v.File.Path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("shared read: %v", err)
	}
	replacement := fileSpec(src, "one", "r2", "renamed", []byte("replaced"))
	replacement.ExpectedEntryID = v.File.EntryID
	replacement.ExpectedRevision = v.File.Revision
	next := createStore(t, m, res, u, replacement, []byte("replaced"))
	if next.File.EntryID != v.File.EntryID {
		t.Fatal("shared rename lost identity")
	}
	got, err = res.Lib.Get(ctx, next.File.Path)
	if err != nil || string(got) != "replaced" {
		t.Fatalf("shared replacement: %q %v", got, err)
	}
	createStore(t, m, res, u, fileSpec(src, "zero", "r1", "zero", nil), nil)
}

func TestSharedReceiptRecoveryDoesNotReactivateUserReplacedReference(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	failOnce := true
	store, err := sharedstore.Open(filepath.Join(dir, "node"), sharedstore.Options{FailureHook: func(point string) error {
		if point == "published" && failOnce {
			failOnce = false
			return errors.New("lost publish response")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	res.Lib.ConfigureShared(u.ID, store, true)
	spec := fileSpec(src, "one", "r1", "one", []byte("original"))
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader([]byte("original"))); err == nil {
		t.Fatal("fault not exercised")
	}
	if _, err := res.Lib.Put(ctx, "Backups/one", []byte("user edit")); err != nil {
		t.Fatal(err)
	}
	recovered, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", nil)
	if err != nil || recovered.Status != "stored" || recovered.File.Revision != 1 {
		t.Fatalf("original receipt lost: %+v %v", recovered, err)
	}
	got, err := res.Lib.Get(ctx, "Backups/one")
	if err != nil || string(got) != "user edit" {
		t.Fatalf("retired wrapper reactivated: %q %v", got, err)
	}
}
