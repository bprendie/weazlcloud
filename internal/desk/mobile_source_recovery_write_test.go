package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileSourceRecoveryWriteBindsCurrentDeviceAndKeepsAlbum(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := us.Create("adoption", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := us.Create("foreign", "test-password", false)
	if err != nil {
		t.Fatal(err)
	}
	old, oldToken, err := us.CreateScopedDevice(owner.ID, "old", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	current, token, err := us.CreateScopedDevice(owner.ID, "current", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	_, readToken, err := us.CreateScopedDevice(owner.ID, "reader", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}
	_, foreignToken, err := us.CreateScopedDevice(foreign.ID, "writer", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := us.Login(owner)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(owner)
	other := h.registry.For(foreign)
	if err = res.Vault.Forge([]byte("owner-vault"), []byte("owner-vault")); err != nil {
		t.Fatal(err)
	}
	if err = other.Vault.Forge([]byte("foreign-vault"), []byte("foreign-vault")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = res.Lib.Drain(ctx)
		_ = other.Lib.Drain(ctx)
		res.LockVault()
		other.LockVault()
	})
	oldOp := catalog.SourceOperation{OperationID: "album", Namespace: "previous", SourceID: "opaque-album", SourceRevision: "source-r1", Kind: "album", Title: "Keep"}
	out, err := res.Lib.ImportPhotoSources(context.Background(), old.ID, []catalog.SourceOperation{oldOp}, false)
	if err != nil || out[0].Status != "applied" {
		t.Fatal(out, err)
	}
	if err = us.RevokeDevice(owner.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	a := catalog.SourceRecoveryAdoption{OperationID: "adopt-1", FromDeviceID: old.ID, FromNamespace: "previous", FromSourceID: "opaque-album", FromSourceRevision: "source-r1", FromServerRevision: 1, Namespace: "current", SourceID: "opaque-album", ServerID: out[0].ServerID, ExpectedServerRevision: 1}
	call := func(credential string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/v1/photos/source-collections/recover", bytes.NewReader(raw))
		r.Header.Set("X-Weazl-Desk", "1")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		return w
	}
	call(readToken, a, 403)
	call(oldToken, a, 401)
	call(foreignToken, a, 404)
	call("", a, 401) // Cookie writes require an explicit current device ID.
	w := call(token, a, 200)
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Header())
	}
	var response struct {
		Mapping catalog.SourceMapping `json:"mapping"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Mapping.DeviceID != current.ID || response.Mapping.ServerID != out[0].ServerID || response.Mapping.SourceRevision != "source-r1" {
		t.Fatal(response)
	}
	call(token, a, 200) // Lost response replays without changing collection content.
	input := struct {
		DeviceID string `json:"device_id"`
		catalog.SourceRecoveryAdoption
	}{current.ID, a}
	call("", input, 200)
	input.DeviceID = old.ID
	call(token, input, 401)
	input.DeviceID = current.ID
	input.ExpectedServerRevision++
	call(token, input, 409)
	got, err := res.Lib.PhotoSourceMapping(context.Background(), current.ID, "current", "opaque-album")
	if err != nil || got.ServerID != out[0].ServerID {
		t.Fatal(got, err)
	}
	capReq := httptest.NewRequest("GET", "/api/v1/mobile/capabilities", nil)
	capReq.Header.Set("Authorization", "Bearer "+token)
	capW := httptest.NewRecorder()
	h.ServeHTTP(capW, capReq)
	if capW.Code != 200 || !bytes.Contains(capW.Body.Bytes(), []byte(`"source_collection_recovery_write":true`)) {
		t.Fatal(capW.Body.String())
	}
	res.LockVault()
	call(token, a, 423)
}
