package photoingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type inspectedSharedReader struct {
	*bytes.Reader
	inspect func()
}

func (r *inspectedSharedReader) Read(p []byte) (int, error) { r.inspect(); return r.Reader.Read(p) }
func TestSharedPartsPreservesBackendAndEncryptedStaging(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err := v.Forge([]byte("shared-parts"), []byte("shared-parts")); err != nil {
		t.Fatal(err)
	}
	shared, err := sharedstore.Open(dir, sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	owner := users.User{ID: strings.Repeat("a", 32)}
	res := &filesvc.Resource{Vault: v, Lib: library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)}
	res.Lib.ConfigureShared(owner.ID, shared, true)
	defer res.LockVault()
	body := bytes.Repeat([]byte("shared-private-original-sentinel-"), 40000)
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	m := New(nil)
	ctx := context.Background()
	sawEncryptedStage := false
	inspect := func() {
		entries, _ := os.ReadDir(filepath.Join(dir, "shared-staging"))
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "source-") {
				continue
			}
			b, e := os.ReadFile(filepath.Join(dir, "shared-staging", entry.Name()))
			if e != nil {
				t.Fatal(e)
			}
			if len(b) > 0 {
				sawEncryptedStage = true
			}
			if bytes.Contains(b, []byte("shared-private-original-sentinel-")) {
				t.Fatal("plaintext shared source staging")
			}
		}
	}
	var firstStats sharedstore.Stats
	var storedPaths []string
	for i, asset := range []string{"one", "two"} {
		spec := Spec{DeviceID: "phone", DeviceAssetID: asset, OriginalMode: "opaque-original-v1", Components: []Component{{ID: "original", Filename: "camera.raw", Size: int64(len(body)), SHA256: digest}}}
		view, err := m.CreateParts(ctx, res, owner, spec)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := m.FinalizeParts(ctx, res, owner, view.Upload.ID, "phone", func(string) (io.ReadCloser, error) {
			return io.NopCloser(&inspectedSharedReader{Reader: bytes.NewReader(body), inspect: inspect}), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		storedPaths = append(storedPaths, stored.Path)
		file, err := res.Lib.Metadata(ctx, stored.Path)
		if err != nil || file.Reference == nil || file.Reference.Backend != catalog.SharedBackend || file.Reference.OwnerEntryID != file.EntryID || file.Reference.OwnerRevision == 0 {
			t.Fatalf("shared reference binding: %+v %v", file, err)
		}
		stats, err := shared.Metrics(ctx)
		if err != nil || stats.LogicalBytes != int64((i+1)*len(body)) || stats.UniqueBytes == 0 {
			t.Fatalf("shared accounting: %+v %v", stats, err)
		}
		if i == 0 {
			firstStats = stats
		} else if stats.UniqueBytes != firstStats.UniqueBytes || stats.Objects != firstStats.Objects {
			t.Fatalf("identical originals duplicated chunks: first=%+v second=%+v", firstStats, stats)
		}
		original, err := res.Lib.Get(ctx, stored.Path)
		if err != nil || !bytes.Equal(original, body) {
			t.Fatalf("shared original: %v", err)
		}
	}
	if !sawEncryptedStage {
		t.Fatal("shared staging was not inspected")
	}
	// Denied publication leaves a verified private component. Cancellation must
	// remove its catalog row and owner reference, preserving both stored originals.
	spec := Spec{DeviceID: "phone", DeviceAssetID: "cancelled", OriginalMode: "opaque-original-v1", Components: []Component{{ID: "original", Filename: "camera.raw", Size: int64(len(body)), SHA256: digest}}}
	view, err := m.CreateParts(ctx, res, owner, spec)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("revoked")
	_, err = m.FinalizePartsGuarded(ctx, res, owner, view.Upload.ID, "phone", func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}, func(func() error) error { return denied })
	if !errors.Is(err, denied) {
		t.Fatalf("publication guard: %v", err)
	}
	receipt, err := load(res, view.Upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := pendingPath(receipt, receipt.Spec.Components[0])
	if _, err := res.Lib.Metadata(ctx, pending); err != nil {
		t.Fatalf("missing private fixture: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := m.Cancel(res, owner, view.Upload.ID, "phone"); err != nil {
			t.Fatalf("idempotent cancellation: %v", err)
		}
	}
	// A restarted coordinator must honor the durable tombstone. A different
	// device cannot repeat cancellation or alter the receipt.
	restarted := New(nil)
	if err := restarted.Cancel(res, owner, view.Upload.ID, "phone"); err != nil {
		t.Fatalf("cancellation after restart: %v", err)
	}
	if err := restarted.Cancel(res, owner, view.Upload.ID, "foreign"); !errors.Is(err, upload.ErrNotFound) {
		t.Fatalf("foreign device cancellation: %v", err)
	}
	cancelled, err := load(res, view.Upload.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.Spec.DeviceAssetID != receipt.Spec.DeviceAssetID {
		t.Fatalf("durable cancellation/source identity: %+v %v", cancelled, err)
	}
	if _, err := res.Lib.Metadata(ctx, pending); !errors.Is(err, library.ErrFileNotFound) {
		t.Fatalf("private row retained: %v", err)
	}
	if _, err := m.CreateParts(ctx, res, owner, spec); !errors.Is(err, upload.ErrNotFound) {
		t.Fatalf("cancelled revision recreated: %v", err)
	}
	if _, err := m.FinalizeParts(ctx, res, owner, view.Upload.ID, "phone", nil); !errors.Is(err, upload.ErrNotFound) {
		t.Fatalf("cancelled revision finalized: %v", err)
	}
	stats, err := shared.Metrics(ctx)
	if err != nil || stats.LogicalBytes != int64(2*len(body)) || stats.UniqueBytes != firstStats.UniqueBytes || stats.Objects != firstStats.Objects {
		t.Fatalf("cancel damaged originals or retained pending reference: %+v %v", stats, err)
	}
	for _, name := range storedPaths {
		original, err := res.Lib.Get(ctx, name)
		if err != nil || !bytes.Equal(original, body) {
			t.Fatalf("private cleanup damaged canonical original %q: %v", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".weazl-photo-components"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("component intent not settled: %v %v", entries, err)
	}
}
