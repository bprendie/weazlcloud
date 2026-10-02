package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileBackupHTTPSourcePartsReceiptIsolation(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := us.Create("native", "native-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(user)
	if err := res.Vault.Forge([]byte("native-vault"), []byte("native-vault")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.LockVault() })
	device, token, err := us.CreateScopedDevice(user.ID, "phone", []string{users.BackupWrite, users.FilesWrite})
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err := us.CreateScopedDevice(user.ID, "other", []string{users.BackupWrite, users.FilesWrite})
	if err != nil {
		t.Fatal(err)
	}
	_, legacy, err := us.CreateDevice(user.ID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := res.Lib.Mkdir(ctx, "Backups"); err != nil {
		t.Fatal(err)
	}
	dest, err := res.Lib.Metadata(ctx, "Backups")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, url, credential string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, url, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Weazl-Desk", "1")
		w := httptest.NewRecorder()
		if !h.tryMobileBackups(w, r) {
			t.Fatal("backup route not dispatched")
		}
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, url, w.Code, want, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("missing private no-store")
		}
		return w
	}
	src := backup.Source{ID: "documents", Name: "Phone Documents", DestinationID: dest.EntryID}
	request("POST", "/api/v1/backups/sources", token, src, 201)
	request("GET", "/api/v1/backups/sources", token, nil, 200)
	request("POST", "/api/v1/backups/sources", legacy, src, 403)
	bad := src
	bad.ID = "foreign-destination"
	bad.DestinationID = "missing-owner-folder"
	request("POST", "/api/v1/backups/sources", token, bad, 404)
	body := []byte("independent parts")
	sum := sha256.Sum256(body)
	spec := backup.Spec{SourceID: src.ID, ItemID: "document", SourceRevision: "r1", RelativePath: "nested/document.txt", Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Transport: "parts-v1"}
	created := request("POST", "/api/v1/backups/uploads", token, spec, 201)
	var view backup.View
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.ReceiptID != view.ID || view.UploadID != "" || view.Spec.Filename != "document.txt" || view.Spec.DeviceID != device.ID {
		t.Fatalf("parts contract: %+v", view)
	}
	sessions, err := h.uploads.List(user)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("parts created plaintext fallback: %v %v", sessions, err)
	}
	route := "/api/v1/backups/uploads/" + view.ID
	request("GET", route, foreign, nil, 404)
	// This is the parent's verified-reader adapter: a registry lease plus a grant
	// callback at source/catalog publication, without legacy byte staging.
	r := httptest.NewRequest("POST", route+"/finalize", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	grant, err := us.GrantForRequest(r, users.BackupWrite, users.FilesWrite)
	if err != nil {
		t.Fatal(err)
	}
	leased, release, ok := h.registry.Enter(ctx, user.ID)
	if !ok {
		t.Fatal("owner unavailable")
	}
	coordinator := backup.New(h.uploads)
	coordinator.SetCommitGuard(func(publish func() error) error {
		return us.WithDeviceGrant(grant, publish, users.BackupWrite, users.FilesWrite)
	})
	stored, err := coordinator.FinalizeParts(leased, res, user, view.ID, device.ID, bytes.NewReader(body))
	release()
	if err != nil || stored.Status != "stored" {
		t.Fatalf("parts adapter: %+v %v", stored, err)
	}
	status := request("GET", route, token, nil, 200)
	if !strings.Contains(status.Body.String(), `"status":"stored"`) {
		t.Fatal("receipt not exposed")
	}
	request("POST", "/api/v1/backups/uploads", token, spec, 201)
	changed := spec
	changed.RelativePath = "other.txt"
	request("POST", "/api/v1/backups/uploads", token, changed, 409)
	request("PATCH", "/api/v1/backups/sources/documents", token, map[string]any{"status": "detached", "expected_revision": 1}, 200)
	got, err := res.Lib.Get(ctx, stored.File.Path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("detach deleted original")
	}
	res.LockVault()
	request("GET", route, token, nil, 423)
}
