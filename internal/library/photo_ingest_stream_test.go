package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Run with restic/restic:0.18.0's binary on PATH, matching deploy/Dockerfile.
// That version rejects nested stdin filenames whose parents do not exist.
func TestPhotoComponentResticUsesFlatStorageKey(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err := v.Forge([]byte("flat-component"), []byte("flat-component")); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)
	defer func() { _ = l.Drain(context.Background()); v.Lock() }()
	ctx := context.Background()
	body := []byte("native original streamed directly to encrypted restic")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	pending := ".weazl-mobile-pending/fixture/original"
	f, err := l.StorePhotoComponent(ctx, pending, bytes.NewReader(body), int64(len(body)), hash)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != pending || f.Reference == nil || f.Reference.Backend != catalog.ResticBackend || f.Reference.Object != f.EntryID || strings.ContainsAny(f.Reference.Object, "/\\") || len(f.Reference.Object) != 32 {
		t.Fatalf("logical path versus flat backend key: %+v", f)
	}
	// The receipt/catalog retry must reuse the snapshot without reading a stream.
	retried, err := l.StorePhotoComponent(ctx, pending, failPhotoComponentReader{t}, int64(len(body)), hash)
	if err != nil || retried.EntryID != f.EntryID || *retried.Reference != *f.Reference {
		t.Fatalf("component retry changed reference: %+v %v", retried, err)
	}
	destination := "Photos/Mobile/phone/asset/1/original.opaque"
	stored, err := l.CommitPhotoIngest(ctx, catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "asset", SourceRevision: "1", OpaqueOriginal: true, Files: []catalog.PhotoIngestFile{{ID: "original", From: pending, To: destination, Hash: hash, Size: int64(len(body)), MediaType: "application/octet-stream"}}})
	if err != nil || stored.Path != destination || *stored.Reference != *f.Reference {
		t.Fatalf("publication changed immutable storage: %+v %v", stored, err)
	}
	original, err := l.Get(ctx, destination)
	if err != nil || !bytes.Equal(original, body) {
		t.Fatalf("read through flat reference: %v", err)
	}
}

type failPhotoComponentReader struct{ t *testing.T }

func (r failPhotoComponentReader) Read([]byte) (int, error) {
	r.t.Error("component retry read stream")
	return 0, io.ErrUnexpectedEOF
}
