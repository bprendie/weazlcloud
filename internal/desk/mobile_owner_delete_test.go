package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileOwnerDeleteDrainsPartsJobsMappingsGrabsAndPreservesSharedNeighbor(t *testing.T) {
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := store.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Create("bob", "bob-password", true)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := sharedstore.Open(dir, sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	q := quota.New(dir)
	registry := filesvc.NewRegistry(store, q)
	registry.ConfigureShared(shared, true)
	caps := capsule.New(filepath.Join(dir, "caps"))
	h := NewMulti(store, caps, q, "", "", dir, registry)
	res, neighbor := registry.For(alice), registry.For(bob)
	for _, resource := range []*filesvc.Resource{res, neighbor} {
		if err := resource.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
			t.Fatal(err)
		}
		defer resource.LockVault()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	payload := []byte("shared immutable original belongs to both owners")
	for _, resource := range []*filesvc.Resource{res, neighbor} {
		if _, err := resource.Lib.Put(ctx, "shared.bin", payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := res.Lib.Put(ctx, "alice-only.bin", []byte("private owner object")); err != nil {
		t.Fatal(err)
	}
	if count, err := shared.ObjectCount(ctx); err != nil || count != 2 {
		t.Fatalf("fixture did not deduplicate shared owners: count=%d err=%v", count, err)
	}
	device, token, err := store.CreateScopedDevice(alice.ID, "phone", []string{users.PhotosWrite, users.FilesWrite, users.BackupWrite})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte, hash string, want int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("X-Weazl-SHA256", hash)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want=%d %s", method, path, w.Code, want, w.Body.String())
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := res.Lib.Mkdir(ctx, "Backups"); err != nil {
		t.Fatal(err)
	}
	root, err := res.Lib.Metadata(ctx, "Backups")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(backup.Source{ID: "documents", Name: "Documents", DestinationID: root.EntryID})
	request("POST", "/api/v1/backups/sources", source, "", 201)
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	spec := backup.Spec{SourceID: "documents", ItemID: "shared", SourceRevision: "r1", RelativePath: "shared.bin", Size: int64(len(payload)), SHA256: hash, Transport: "parts-v1"}
	coordinator := backup.New(h.uploads)
	v, err := coordinator.CreateParts(ctx, res, alice, device.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if v, err = coordinator.FinalizeParts(ctx, res, alice, v.ID, device.ID, bytes.NewReader(payload)); err != nil || v.Status != "stored" {
		t.Fatalf("durable source mapping: %+v %v", v, err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "users", alice.ID, ".weazl-backups")); err != nil || len(entries) < 2 {
		t.Fatalf("source/mapping receipts not created: %v", err)
	}
	photo := map[string]any{"transport": "parts-v1", "commit_when_complete": true, "device_asset_id": "active", "filename": "camera.raw", "size": len(payload), "sha256": hash, "original_mode": "opaque-original-v1"}
	raw, _ := json.Marshal(photo)
	created := request("POST", "/api/v1/photos/uploads", raw, "", 201)
	var active mobileparts.View
	if err := json.Unmarshal(created["transfer"], &active); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/api/v1/photos/uploads/"+active.ID+"/components/original/parts/0", payload, hash, 200)
	photo["device_asset_id"] = "pending"
	raw, _ = json.Marshal(photo)
	queued := request("POST", "/api/v1/photos/uploads", raw, "", 201)
	var pending mobileparts.View
	if err := json.Unmarshal(queued["transfer"], &pending); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/api/v1/photos/uploads/"+pending.ID+"/components/original/parts/0", payload, hash, 200)
	archive, err := res.Archives.Start([]string{"shared.bin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []users.User{alice, bob} {
		if _, err := caps.MintStream(capsule.Record{Owner: owner.ID, Name: "frozen.bin", Gate: "open", Limit: 2, Expires: time.Now().Add(time.Hour)}, "", func(w io.Writer) error { _, err := w.Write(payload); return err }); err != nil {
			t.Fatal(err)
		}
	}
	aliceGrab, bobGrab := caps.ListOwner(alice.ID)[0], caps.ListOwner(bob.ID)[0]
	if status, err := q.Status(store.Count()); err != nil || status.Reserved == 0 {
		t.Fatalf("mobile jobs hold no reservation: %+v %v", status, err)
	}
	lease, release, ok := registry.Enter(ctx, alice.ID)
	if !ok {
		t.Fatal("owner lease unavailable")
	}
	started, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(finish) })
	processed := make(chan error, 1)
	go func() {
		defer release()
		processed <- h.mobileParts.Process(lease, res, active.ID, h.authorizeMobilePart, func(_ mobileparts.Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
			reader, err := open("original")
			if err != nil {
				return nil, err
			}
			defer reader.Close()
			close(started)
			<-lease.Done()
			<-finish
			_, err = reader.Read(make([]byte, 1))
			return nil, err
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("active finalizer did not enter commit")
	}
	deleted := make(chan error, 1)
	go func() { deleted <- h.accounts.Delete(ctx, alice.ID) }()
	select {
	case <-lease.Done():
	case <-ctx.Done():
		t.Fatal("delete did not cancel active owner lease")
	}
	select {
	case err := <-deleted:
		t.Fatalf("delete completed before worker released its resource: %v", err)
	default:
	}
	if _, ok := store.ActiveDevice(alice.ID, device.ID); ok {
		t.Fatal("draining owner still has active device")
	}
	once.Do(func() { close(finish) })
	if err := <-processed; !errors.Is(err, context.Canceled) {
		t.Fatalf("active reader was not cancelled: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, ok := store.User(alice.ID); ok {
		t.Fatal("deleted account remains")
	}
	for _, path := range []string{filepath.Join(dir, "users", alice.ID), filepath.Join(dir, "caps", aliceGrab.ID)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted private state survives %s: %v", path, err)
		}
	}
	if _, _, ok := res.Archives.Get(archive.ID); ok {
		t.Fatal("deleted archive job survives")
	}
	if len(caps.ListOwner(alice.ID)) != 0 || len(caps.ListOwner(bob.ID)) != 1 || caps.ListOwner(bob.ID)[0].ID != bobGrab.ID {
		t.Fatal("grab owner cleanup crossed owner boundary")
	}
	if status, err := q.Status(store.Count()); err != nil || status.Reserved != 0 {
		t.Fatalf("deleted owner leaked staging/job reservations: %+v %v", status, err)
	}
	if _, err := shared.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := shared.ObjectCount(ctx); err != nil || count != 1 {
		t.Fatalf("owner-only shared object not collected or neighbor object lost: %d %v", count, err)
	}
	if metrics, err := shared.Metrics(ctx); err != nil || metrics.UniqueBytes != int64(len(payload)) || metrics.LogicalBytes != int64(len(payload)) {
		t.Fatalf("deleted owner references survive or neighbor reference lost: %+v %v", metrics, err)
	}
	got, err := neighbor.Lib.Get(ctx, "shared.bin")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("surviving owner's deduped original changed: %v", err)
	}
	if _, err := h.uploads.Create(bob, "still-available.bin", 1, ""); err != nil {
		t.Fatalf("surviving owner upload unavailable: %v", err)
	}
}
