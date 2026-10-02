package backup

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/upload"
)

func TestIndependentIntentsShareOnlyOwnedParents(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	first, err := m.CreateParts(ctx, res, u, "phone", fileSpec(src, "one", "r1", "nested/one", []byte("one")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateParts(ctx, res, u, "phone", fileSpec(src, "two", "r1", "nested/two", []byte("two")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, first.ID, "phone", bytes.NewReader([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, second.ID, "phone", bytes.NewReader([]byte("two"))); err != nil {
		t.Fatal(err)
	}
	parent, err := res.Lib.Metadata(ctx, "Backups/nested")
	if err != nil {
		t.Fatal(err)
	}
	if parent.Revision != 1 {
		t.Fatal("shared parent was rewritten")
	}
}

func TestOwnedFolderRenameAndUnrelatedChildConflict(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	folder := createStore(t, m, res, u, Spec{SourceID: src.ID, ItemID: "folder", SourceRevision: "r1", RelativePath: "tree", Kind: "folder"}, nil)
	child := createStore(t, m, res, u, fileSpec(src, "child", "r1", "tree/child", []byte("child")), []byte("child"))
	s := Spec{SourceID: src.ID, ItemID: "folder", SourceRevision: "r2", RelativePath: "renamed", Kind: "folder", ExpectedEntryID: folder.File.EntryID, ExpectedRevision: folder.File.Revision}
	moved := createStore(t, m, res, u, s, nil)
	if moved.File.EntryID != folder.File.EntryID {
		t.Fatal("folder ID changed")
	}
	c, err := res.Lib.Metadata(ctx, "Backups/renamed/child")
	if err != nil || c.EntryID != child.File.EntryID || c.Revision != child.File.Revision+1 {
		t.Fatalf("child rename: %+v %v", c, err)
	}
	// A revised child operation targets its moved mapping, not its old path.
	replace := fileSpec(src, "child", "r2", "renamed/child", []byte("new"))
	replace.ExpectedEntryID = c.EntryID
	replace.ExpectedRevision = c.Revision
	createStore(t, m, res, u, replace, []byte("new"))
	if _, err := res.Lib.Put(ctx, "Backups/renamed/user-owned", []byte("user")); err != nil {
		t.Fatal(err)
	}
	s.SourceRevision = "r3"
	s.RelativePath = "again"
	s.ExpectedRevision = moved.File.Revision
	if _, err := m.CreateParts(ctx, res, u, "phone", s); !errors.Is(err, ErrStale) {
		t.Fatalf("unrelated child renamed: %v", err)
	}
}

func TestSharedUnpublishedPrepareAbortedOnRestartRetriesSameReceipt(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	store, err := sharedstore.Open(filepath.Join(dir, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	res.Lib.ConfigureShared(u.ID, store, true)
	body := []byte("prepared bytes")
	spec := fileSpec(src, "one", "r1", "one", body)
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	err = with(ctx, res, u, func(tx *library.BackupTransaction) error {
		op, err := loadOperation(tx, v.ID, "phone")
		if err != nil {
			return err
		}
		for i, f := range op.Plan.Files {
			if f.EntryID == op.PrimaryID {
				f, err = tx.StoreReader(op.ID, f, bytes.NewReader(body))
				if err != nil {
					return err
				}
				op.Plan.Files[i] = f
			}
		}
		op.Status = "publishing"
		return tx.Write(op.ID, op)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Lib.ReconcileShared(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != v.ID || result.File.Reference.Operation == v.ID {
		t.Fatal("storage retry did not preserve receipt/change aborted attempt")
	}
	got, err := res.Lib.Get(ctx, result.File.Path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("retried shared read: %v", err)
	}
}

func TestSequentialFallbackAndChecksumConflict(t *testing.T) {
	_, res, u, src, dir := fixture(t)
	ctx := context.Background()
	uploads := upload.New(filepath.Join(dir, "uploads"), nil, nil, nil)
	m := New(uploads)
	body := []byte("ordered fallback")
	s := fileSpec(src, "one", "r1", "one", body)
	s.Transport = "sequential"
	v, err := m.Create(ctx, res, u, "phone", s)
	if err != nil || v.UploadID == "" {
		t.Fatalf("fallback create: %+v %v", v, err)
	}
	if _, err := m.Append(ctx, res, u, v.ID, "phone", 0, int64(len(body)), s.SHA256, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	result, err := m.Finalize(ctx, res, u, v.ID, "phone")
	if err != nil || result.Status != "stored" {
		t.Fatalf("fallback finalize: %+v %v", result, err)
	}
	s = fileSpec(src, "other", "r1", "other", []byte("correct"))
	s.Transport = "parts-v1"
	v, err = m.CreateParts(ctx, res, u, "phone", s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader([]byte("wrong!!"))); !errors.Is(err, ErrChecksum) {
		t.Fatalf("bad reader published: %v", err)
	}
	if _, err := res.Lib.Metadata(ctx, "Backups/other"); err == nil {
		t.Fatal("checksum failure visible in catalog")
	}
}

func TestDeviceValidityGuardBlocksPublish(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	v, err := m.CreateParts(ctx, res, u, "phone", fileSpec(src, "one", "r1", "one", []byte("one")))
	if err != nil {
		t.Fatal(err)
	}
	revoked := errors.New("device revoked")
	m.SetCommitGuard(func(func() error) error { return revoked })
	if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader([]byte("one"))); !errors.Is(err, revoked) {
		t.Fatalf("revoked publish: %v", err)
	}
	if _, err := res.Lib.Metadata(ctx, "Backups/one"); err == nil {
		t.Fatal("revoked device published")
	}
}

func TestSourceAndIntentMutationsUseRequestGuard(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	denied := errors.New("credential invalidated")
	m.SetCommitGuard(func(func() error) error { return denied })
	if _, err := m.UpdateSource(ctx, res, u, "phone", src.ID, "detached", src.Revision); !errors.Is(err, denied) {
		t.Fatalf("unguarded detach: %v", err)
	}
	other := src
	other.ID = "other"
	if _, err := m.Register(ctx, res, u, "phone", other); !errors.Is(err, denied) {
		t.Fatalf("unguarded register: %v", err)
	}
	if _, err := m.CreateParts(ctx, res, u, "phone", fileSpec(src, "one", "r1", "one", []byte("one"))); !errors.Is(err, denied) {
		t.Fatalf("unguarded intent: %v", err)
	}
	m = New(nil)
	sources, _, err := m.Sources(ctx, res, u, "phone", "")
	if err != nil || len(sources) != 1 || sources[0].Status != "active" {
		t.Fatalf("rejected guard mutated registry: %+v %v", sources, err)
	}
}
