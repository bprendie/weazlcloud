package library

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type isolatedLegacy struct {
	root  string
	files map[string][]byte
}

func (b *isolatedLegacy) Ensure(context.Context) error { return os.MkdirAll(b.root, 0o700) }
func (b *isolatedLegacy) Put(_ context.Context, name string, r io.Reader) (catalog.Reference, error) {
	data, e := io.ReadAll(r)
	if e != nil {
		return catalog.Reference{}, e
	}
	id := name + "-legacy"
	if b.files == nil {
		b.files = map[string][]byte{}
	}
	b.files[id] = data
	if err := os.WriteFile(filepath.Join(b.root, id), data, 0o600); err != nil {
		return catalog.Reference{}, err
	}
	return catalog.Reference{Backend: catalog.ResticBackend, Version: 1, Snapshot: id, Object: id}, nil
}
func (b *isolatedLegacy) PutBatch(context.Context, string) (BatchReference, error) {
	return BatchReference{}, nil
}
func (b *isolatedLegacy) Capture(f catalog.File) (catalog.Reference, error) { return fileReference(f) }
func (b *isolatedLegacy) Read(_ context.Context, r catalog.Reference, w io.Writer) error {
	data := b.files[r.Object]
	if data == nil {
		var err error
		data, err = os.ReadFile(filepath.Join(b.root, r.Object))
		if err != nil {
			return err
		}
	}
	_, e := w.Write(data)
	return e
}
func (b *isolatedLegacy) ReadRange(ctx context.Context, r catalog.Reference, o, n int64, w io.Writer) error {
	x := b.files[r.Object]
	if o < 0 || n < 0 || o+n > int64(len(x)) {
		return io.ErrUnexpectedEOF
	}
	_, e := w.Write(x[o : o+n])
	return e
}
func (b *isolatedLegacy) Snapshots(context.Context) ([]string, error)        { return nil, nil }
func (b *isolatedLegacy) Forget(context.Context, []string) ([]string, error) { return nil, nil }
func (b *isolatedLegacy) Hold(catalog.Reference) (func(), error)             { return func() {}, nil }
func (b *isolatedLegacy) Drain(context.Context) error                        { return nil }

func TestSharedBackendMixedLibraryLifecycle(t *testing.T) {
	base := os.Getenv("WEAZLCLOUD_SHAREDSTORE_TEST_ROOT")
	if base == "" {
		t.Skip("run scripts/sharedstore-smoke.sh for volume-backed shared integration")
	}
	root, err := os.MkdirTemp(base, "library-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx := context.Background()
	store, err := sharedstore.Open(filepath.Join(root, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	makeLib := func(owner string) (*Library, *vault.Vault) {
		dir := filepath.Join(root, "users", owner)
		_ = os.MkdirAll(dir, 0o700)
		v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
		if e := v.Forge([]byte("test-pass-"+owner), []byte("test-pass-"+owner)); e != nil {
			t.Fatal(e)
		}
		lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
		lib.backend = &isolatedLegacy{root: filepath.Join(dir, "repo")}
		lib.ConfigureShared(owner, store, false)
		return lib, v
	}
	alice, aliceVault := makeLib("alice")
	bob, bobVault := makeLib("bob")
	legacy, err := alice.Put(ctx, "legacy.bin", []byte("restic side"))
	if err != nil {
		t.Fatal(err)
	}
	alice.ConfigureShared("alice", store, true)
	payload := bytes.Repeat([]byte("same shared payload"), 1024)
	shared, err := alice.Put(ctx, "same.bin", payload)
	if err != nil {
		t.Fatal(err)
	}
	if shared.Reference == nil || shared.Reference.Backend != catalog.SharedBackend {
		t.Fatalf("not shared: %+v", shared.Reference)
	}
	bob.ConfigureShared("bob", store, true)
	if _, err = bob.Put(ctx, "same.bin", payload); err != nil {
		t.Fatal(err)
	}
	previewImage := image.NewRGBA(image.Rect(0, 0, 3, 2))
	previewImage.Set(1, 1, color.RGBA{R: 120, B: 240, A: 255})
	var pngBytes bytes.Buffer
	if err = png.Encode(&pngBytes, previewImage); err != nil {
		t.Fatal(err)
	}
	if _, err = alice.Put(ctx, "preview.png", pngBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	thumb, thumbType, err := alice.Thumbnail(ctx, "preview.png", 128)
	if err != nil || thumbType != "image/png" {
		t.Fatalf("shared preview type=%q err=%v", thumbType, err)
	}
	if _, err = png.Decode(bytes.NewReader(thumb)); err != nil {
		t.Fatalf("shared thumbnail is invalid: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sharedstore.Open(filepath.Join(root, "node"), sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	aliceVault.Lock()
	bobVault.Lock()
	if err = aliceVault.Unlock([]byte("test-pass-alice")); err != nil {
		t.Fatal(err)
	}
	if err = bobVault.Unlock([]byte("test-pass-bob")); err != nil {
		t.Fatal(err)
	}
	newLib := func(owner string, v *vault.Vault) *Library {
		dir := filepath.Join(root, "users", owner)
		lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
		lib.backend = &isolatedLegacy{root: filepath.Join(dir, "repo")}
		lib.ConfigureShared(owner, store, true)
		return lib
	}
	alice, bob = newLib("alice", aliceVault), newLib("bob", bobVault)
	thumb, thumbType, err = alice.Thumbnail(ctx, "preview.png", 128)
	if err != nil || thumbType != "image/png" {
		t.Fatalf("shared preview after reopen type=%q err=%v", thumbType, err)
	}
	if _, err = png.Decode(bytes.NewReader(thumb)); err != nil {
		t.Fatalf("shared preview after reopen is invalid: %v", err)
	}
	for _, tc := range []struct {
		lib  *Library
		path string
		want []byte
	}{{alice, "legacy.bin", []byte("restic side")}, {alice, "same.bin", payload}, {bob, "same.bin", payload}} {
		var out bytes.Buffer
		if err = tc.lib.StreamTo(ctx, tc.path, &out); err != nil || !bytes.Equal(out.Bytes(), tc.want) {
			t.Fatalf("mixed read %s: %v", tc.path, err)
		}
	}
	var ranged bytes.Buffer
	if err = alice.StreamRange(ctx, "same.bin", 7, 103, &ranged); err != nil || !bytes.Equal(ranged.Bytes(), payload[7:110]) {
		t.Fatalf("range failed: %v", err)
	}
	if err = alice.Copy(ctx, "same.bin", "copy.bin"); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if err = alice.StreamTo(ctx, "copy.bin", &copied); err != nil || !bytes.Equal(copied.Bytes(), payload) {
		t.Fatalf("copy grant failed: %v", err)
	}
	manifest, err := alice.PrepareArchive(ctx, []string{"legacy.bin", "same.bin"})
	if err != nil {
		t.Fatal(err)
	}
	newPayload := []byte("replacement version")
	if _, err = alice.Put(ctx, "same.bin", newPayload); err != nil {
		t.Fatal(err)
	}
	var zipBytes bytes.Buffer
	if _, _, err = alice.WriteArchive(ctx, manifest, &zipBytes); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(zipBytes.Bytes()), int64(zipBytes.Len()))
	if err != nil {
		t.Fatal(err)
	}
	archiveContents := make(map[string][]byte)
	for _, zipped := range archive.File {
		entry, openErr := zipped.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		body, readErr := io.ReadAll(entry)
		_ = entry.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		archiveContents[zipped.Name] = body
	}
	if !bytes.Equal(archiveContents["same.bin"], payload) || !bytes.Equal(archiveContents["legacy.bin"], []byte("restic side")) {
		t.Fatal("mixed archive lost its Restic entry or followed replacement instead of captured shared content")
	}
	var liveOld bytes.Buffer
	if err = alice.StreamTo(ctx, "copy.bin", &liveOld); err != nil || !bytes.Equal(liveOld.Bytes(), payload) {
		t.Fatalf("second owner reference was damaged: %v", err)
	}
	pct, logical, unique, err := alice.Dedupe(ctx)
	if err != nil || logical <= unique || pct == 0 {
		t.Fatalf("global aggregate stats: %d %d %d %v", pct, logical, unique, err)
	}
	if err = alice.Delete("copy.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err = alice.CleanupTrash(ctx, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = alice.StreamTo(ctx, "legacy.bin", io.Discard); err != nil {
		t.Fatalf("legacy ref was lost during mixed cleanup: %v", err)
	}
	_ = legacy
}
