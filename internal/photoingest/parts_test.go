package photoingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPartsReceiptHasNoLegacySessionsAndRecoversPublication(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err := v.Forge([]byte("parts-test"), []byte("parts-test")); err != nil {
		t.Fatal(err)
	}
	res := &filesvc.Resource{Vault: v, Lib: library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)}
	defer res.LockVault()
	m := New(nil)
	ctx := context.Background()
	user := users.User{ID: "owner"}
	body := []byte("opaque camera original")
	hash := sha256.Sum256(body)
	spec := Spec{DeviceID: "phone", SourceNamespace: "photokit", SourceAssetID: "opaque/asset+id==", Hidden: true, OriginalMode: "opaque-original-v1", Components: []Component{{ID: "original", Filename: "camera.raw", Size: int64(len(body)), SHA256: hex.EncodeToString(hash[:])}}}
	view, err := m.CreateParts(ctx, res, user, spec)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := load(res, view.Upload.ID)
	if err != nil || receipt.Uploads[0] != "" {
		t.Fatalf("legacy session allocated: %+v %v", receipt, err)
	}
	badOpen := func(component string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("!"), len(body)))), nil
	}
	if _, err = m.FinalizeParts(ctx, res, user, view.Upload.ID, "phone", badOpen); !errors.Is(err, upload.ErrHashMismatch) {
		t.Fatalf("checksum: %v", err)
	}
	current, err := m.Status(res, user, view.Upload.ID, "phone")
	if err != nil || current.Status == "stored" {
		t.Fatalf("bad bytes published: %+v %v", current, err)
	}
	opens := 0
	open := func(component string) (io.ReadCloser, error) {
		opens++
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	revoked := errors.New("device revoked")
	if _, err = m.FinalizePartsGuarded(ctx, res, user, view.Upload.ID, "phone", open, func(publish func() error) error { return revoked }); !errors.Is(err, revoked) {
		t.Fatalf("guard: %v", err)
	}
	page, err := res.Lib.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("revoked publication leaked: %+v %v", page, err)
	}
	stored, err := m.FinalizeParts(ctx, res, user, view.Upload.ID, "phone", open)
	if err != nil || stored.Status != "stored" || stored.ProcessingState != "unsupported" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	mapping, e := res.Lib.PhotoSourceMapping(ctx, "phone", "photokit", "opaque/asset+id==")
	if e != nil || mapping.ServerID != stored.AssetID || mapping.SourceID != "opaque/asset+id==" {
		t.Fatalf("source mapping: %+v %v", mapping, e)
	}
	// Revert only receipt to simulate crash after catalog publication, before save.
	if err = save(res, receipt); err != nil {
		t.Fatal(err)
	}
	if err = m.Cancel(res, user, view.Upload.ID, "phone"); !errors.Is(err, upload.ErrIdempotencyConflict) {
		t.Fatalf("cancel after catalog publication: %v", err)
	}
	current, err = m.Status(res, user, view.Upload.ID, "phone")
	if err != nil || current.Status != "stored" || current.AssetID != stored.AssetID {
		t.Fatalf("cancel failed to recover stored receipt: %+v %v", current, err)
	}
	// Exercise finalize recovery independently from cancellation recovery.
	if err = save(res, receipt); err != nil {
		t.Fatal(err)
	}
	recovered, err := m.FinalizeParts(ctx, res, user, view.Upload.ID, "phone", open)
	if err != nil || recovered.AssetID != stored.AssetID || recovered.Revision != stored.Revision || opens != 2 {
		t.Fatalf("recovered: %+v opens=%d %v", recovered, opens, err)
	}
	if _, err = m.FinalizeParts(ctx, res, user, view.Upload.ID, "foreign", open); err == nil {
		t.Fatal("foreign device accepted")
	}
	original, err := res.Lib.Get(ctx, stored.Path)
	if err != nil || !bytes.Equal(original, body) {
		t.Fatalf("original: %q %v", original, err)
	}
	files, err := os.ReadDir(res.Lib.PhotoIngestDir())
	if err != nil || len(files) != 1 {
		t.Fatalf("receipts: %+v %v", files, err)
	}
}
