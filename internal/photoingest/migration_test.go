package photoingest

import (
	"bytes"
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhotoReceiptFutureSchemaRejectedWithoutRewrite(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err := v.Forge([]byte("receipt-test"), []byte("receipt-test")); err != nil {
		t.Fatal(err)
	}
	res := &filesvc.Resource{Vault: v, Lib: library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)}
	defer res.LockVault()
	spec := Spec{DeviceID: "phone", DeviceAssetID: "asset", Transport: "parts-v1", Components: []Component{{ID: "original", Filename: "a.jpg", MediaType: "image/jpeg", Size: 1, SHA256: strings.Repeat("a", 64)}}}
	// The receipt identity is independent of current format; emulate a newer writer.
	identity := []byte(`["phone","asset","1"]`)
	fingerprint, err := v.Fingerprint("photo-ingest", identity)
	if err != nil {
		t.Fatal(err)
	}
	const digits = "0123456789abcdef"
	id := ""
	for _, b := range fingerprint[:16] {
		id += string([]byte{digits[b>>4], digits[b&15]})
	}
	clear(fingerprint)
	receipt := Receipt{Version: 2, ID: id, Spec: spec, Uploads: []string{""}, Status: "stored"}
	if err = save(res, receipt); err != nil {
		t.Fatal(err)
	}
	path := receiptPath(res, id)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(nil)
	if _, err = m.Status(res, users.User{ID: "owner"}, id, "phone"); !errors.Is(err, upload.ErrCorrupt) {
		t.Fatalf("future receipt status: %v", err)
	}
	if _, err = m.FinalizeParts(context.Background(), res, users.User{ID: "owner"}, id, "phone", nil); !errors.Is(err, upload.ErrCorrupt) {
		t.Fatalf("future receipt finalize: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("future receipt rewritten")
	}
}
