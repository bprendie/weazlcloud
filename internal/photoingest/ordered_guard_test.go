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
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOrderedPhotoFinalizeRejectsGrantRevokedAfterVerification(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create("ordered", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := store.CreateScopedDevice(owner.ID, "phone", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/v1/photos/uploads/abc/finalize", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	grant, err := store.GrantForRequest(request, users.PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err = v.Forge([]byte("guard-test"), []byte("guard-test")); err != nil {
		t.Fatal(err)
	}
	res := &filesvc.Resource{Vault: v, Lib: library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)}
	defer res.LockVault()
	uploads := upload.New(filepath.Join(dir, "uploads"), func(users.User) *filesvc.Resource { return res }, nil, nil)
	m := New(uploads)
	ctx := context.Background()
	body := []byte("opaque ordered original")
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	spec := Spec{DeviceID: device.ID, DeviceAssetID: "asset", OriginalMode: "opaque-original-v1", Filename: "a.raw", Size: int64(len(body)), SHA256: digest}
	view, err := m.Create(ctx, res, owner, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Append(ctx, res, owner, view.Upload.ID, device.ID, "original", 0, int64(len(body)), digest, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	checked := false
	guard := func(publish func() error) error {
		checked = true
		// This hook runs after verified streams have reached private pending storage.
		if err := store.RevokeDevice(owner.ID, device.ID); err != nil {
			return err
		}
		return store.WithDeviceGrant(grant, publish, users.PhotosWrite)
	}
	if _, err = m.FinalizeGuarded(ctx, res, owner, view.Upload.ID, device.ID, nil, guard); !errors.Is(err, users.ErrNoSession) {
		t.Fatalf("revoked finalize: %v", err)
	}
	if !checked {
		t.Fatal("publication guard skipped")
	}
	receipt, err := load(res, view.Upload.ID)
	if err != nil || receipt.Status == "stored" || receipt.AssetID != "" {
		t.Fatalf("revoked receipt published: %+v %v", receipt, err)
	}
	page, err := res.Lib.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("revoked original visible: %+v %v", page, err)
	}
	pending, err := res.Lib.Metadata(ctx, pendingPath(receipt, receipt.Spec.Components[0]))
	if err != nil || pending.Hash != digest {
		t.Fatalf("verification stage lost: %+v %v", pending, err)
	}
}
