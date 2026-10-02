package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func fixture(t *testing.T) (*Manager, *filesvc.Resource, users.User, Source, string) {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("backup-test"), []byte("backup-test")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	user := users.User{ID: "0123456789abcdef0123456789abcdef"}
	lib.ConfigureShared(user.ID, nil, false)
	res := &filesvc.Resource{Vault: v, Lib: lib}
	if err := lib.Mkdir(context.Background(), "Backups"); err != nil {
		t.Fatal(err)
	}
	root, err := lib.Metadata(context.Background(), "Backups")
	if err != nil {
		t.Fatal(err)
	}
	m := New(nil)
	src, err := m.Register(context.Background(), res, user, "phone", Source{ID: "source/raw opaque", Name: "Documents", DestinationID: root.EntryID})
	if err != nil {
		t.Fatal(err)
	}
	return m, res, user, src, dir
}
func fileSpec(src Source, item, revision, p string, body []byte) Spec {
	hash := sha256.Sum256(body)
	return Spec{SourceID: src.ID, ItemID: item, SourceRevision: revision, RelativePath: p, Size: int64(len(body)), SHA256: hex.EncodeToString(hash[:]), Transport: "parts-v1"}
}
func createStore(t *testing.T, m *Manager, res *filesvc.Resource, u users.User, s Spec, body []byte) View {
	t.Helper()
	ctx := context.Background()
	v, err := m.CreateParts(ctx, res, u, "phone", s)
	if err != nil {
		t.Fatal(err)
	}
	if v.UploadID != "" {
		t.Fatal("parts created legacy plaintext upload")
	}
	v, err = m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "stored" || v.File == nil {
		t.Fatalf("missing receipt: %+v", v)
	}
	return v
}
func TestPartsRestartReceiptsReplacementAndOneWayDetach(t *testing.T) {
	m, res, u, src, dir := fixture(t)
	ctx := context.Background()
	body := []byte("private backup bytes")
	s := fileSpec(src, "opaque item / one", "r1", "nested/file.txt", body)
	first := createStore(t, m, res, u, s, body)
	// A new manager/library simulates restart. The same revision must return the
	// original receipt without reading another payload, even after user changes.
	res.Lib = library.New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), res.Vault)
	res.Lib.ConfigureShared(u.ID, nil, false)
	m = New(nil)
	retry, err := m.CreateParts(ctx, res, u, "phone", s)
	if err != nil || retry.ID != first.ID || retry.File.EntryID != first.File.EntryID {
		t.Fatalf("restart retry: %+v %v", retry, err)
	}
	retry, err = m.FinalizeParts(ctx, res, u, first.ID, "phone", nil)
	if err != nil || retry.File.Revision != first.File.Revision {
		t.Fatalf("lost response retry: %v", err)
	}
	changed := s
	changed.RelativePath = "elsewhere.txt"
	if _, err := m.CreateParts(ctx, res, u, "phone", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed same revision: %v", err)
	}
	secondSpec := fileSpec(src, s.ItemID, "r2", "nested/renamed.txt", []byte("replacement"))
	secondSpec.ExpectedEntryID = first.File.EntryID
	secondSpec.ExpectedRevision = first.File.Revision
	second := createStore(t, m, res, u, secondSpec, []byte("replacement"))
	if second.File.EntryID != first.File.EntryID || second.File.Revision != first.File.Revision+1 {
		t.Fatal("rename replaced stable identity")
	}
	if _, err := res.Lib.Metadata(ctx, first.File.Path); err == nil {
		t.Fatal("old rename path remains")
	}
	if _, err := m.UpdateSource(ctx, res, u, "phone", src.ID, "detached", src.Revision); err != nil {
		t.Fatal(err)
	}
	got, err := res.Lib.Get(ctx, second.File.Path)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("detach removed bytes: %q %v", got, err)
	}
	third := secondSpec
	third.SourceRevision = "r3"
	third.ExpectedRevision = second.File.Revision
	if _, err := m.CreateParts(ctx, res, u, "phone", third); !errors.Is(err, ErrPaused) {
		t.Fatalf("detached accepted: %v", err)
	}
	// All durable source names/specs/receipts are encrypted at rest.
	entries, err := os.ReadDir(filepath.Join(dir, ".weazl-backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, ".weazl-backups", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("opaque item")) || bytes.Contains(raw, []byte("Documents")) {
			t.Fatal("private registry plaintext")
		}
	}
}

func TestZeroByteNestedEmptyFolderAndDeviceIsolation(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	empty := createStore(t, m, res, u, fileSpec(src, "empty", "r1", "a/b/zero", nil), nil)
	got, err := res.Lib.Get(ctx, empty.File.Path)
	if err != nil || len(got) != 0 {
		t.Fatalf("zero byte read: %v", err)
	}
	folder := Spec{SourceID: src.ID, ItemID: "folder", SourceRevision: "r1", RelativePath: "a/b/empty", Kind: "folder"}
	v := createStore(t, m, res, u, folder, nil)
	if !v.File.Folder {
		t.Fatal("empty folder not stored")
	}
	if _, err := m.Status(ctx, res, u, v.ID, "another-phone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign device receipt: %v", err)
	}
	other := u
	other.ID = "foreign"
	if _, err := m.Status(ctx, res, other, v.ID, "phone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner receipt: %v", err)
	}
	res.Vault.Lock()
	if _, err := m.Status(ctx, res, u, v.ID, "phone"); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked receipt: %v", err)
	}
}

func TestConflictsNeverResurrectOrOverwrite(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	first := createStore(t, m, res, u, fileSpec(src, "one", "r1", "one", []byte("original")), []byte("original"))
	replacement := fileSpec(src, "one", "r2", "one", []byte("new"))
	replacement.ExpectedEntryID = first.File.EntryID
	replacement.ExpectedRevision = first.File.Revision
	pending, err := m.CreateParts(ctx, res, u, "phone", replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Lib.Delete(first.File.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, pending.ID, "phone", bytes.NewReader([]byte("new"))); !errors.Is(err, ErrStale) {
		t.Fatalf("trash resurrected: %v", err)
	}
	if err := res.Lib.Restore(ctx, first.File.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FinalizeParts(ctx, res, u, pending.ID, "phone", bytes.NewReader([]byte("new"))); !errors.Is(err, ErrStale) {
		t.Fatalf("restore overwritten: %v", err)
	}
	s := fileSpec(src, "different item", "r1", "one", []byte("intruder"))
	if _, err := m.CreateParts(ctx, res, u, "phone", s); err == nil {
		t.Fatal("unrelated collision accepted")
	}
	if err := res.Lib.Rename(ctx, "Backups", "Moved"); err != nil {
		t.Fatal(err)
	}
	s.RelativePath = "new"
	if _, err := m.CreateParts(ctx, res, u, "phone", s); !errors.Is(err, ErrStale) {
		t.Fatalf("moved destination accepted: %v", err)
	}
}
