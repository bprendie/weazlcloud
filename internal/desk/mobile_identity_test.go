package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileIdentityDiscoveryEnrollmentAndLockedCapabilities(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	us.SetClock(func() time.Time { return now })
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	cookie, err := us.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, token string, session bool, want int) map[string]json.RawMessage {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("X-Weazl-Desk", "1")
		if session {
			r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		if !h.tryMobileIdentity(w, r) {
			t.Fatal("identity route not handled", path)
		}
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d", method, path, w.Code, want)
		}
		if want == 200 && w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("missing private cache policy", path)
		}
		out := map[string]json.RawMessage{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := request("GET", "/.well-known/weazlcloud", nil, "", false, 200)
	request("GET", "/api/v1/mobile/capabilities", nil, "", false, 401)
	request("POST", "/api/v1/devices", map[string]any{"name": "locked", "scopes": []string{users.FilesRead}}, "", true, 401)
	res := h.registry.For(u)
	if err := res.Vault.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	if err := res.Vault.Unlock([]byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.LockVault() })
	legacy := request("POST", "/api/v1/devices", map[string]string{"name": "legacy"}, "", true, 201)
	if string(legacy["scope"]) != `"photos:v1"` {
		t.Fatal("legacy enrollment compatibility")
	}
	enrolled := request("POST", "/api/v1/devices", map[string]any{"name": "phone", "scopes": []string{users.PhotosRead, users.FilesRead}}, "", true, 201)
	var token string
	var device users.Device
	if err := json.Unmarshal(enrolled["token"], &token); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(enrolled["device"], &device); err != nil {
		t.Fatal(err)
	}
	if !users.HasScope(device, users.FilesRead) || users.HasScope(device, users.PhotosWrite) {
		t.Fatal("incorrect enrollment grants")
	}
	other, _, err := us.CreateDevice(u.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	list := request("GET", "/api/v1/devices", nil, token, false, 200)
	var devices []users.Device
	if err := json.Unmarshal(list["devices"], &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != device.ID {
		t.Fatal("bearer enumerated other devices")
	}
	request("GET", "/api/v1/devices/"+other.ID, nil, token, false, 403)
	request("POST", "/api/v1/devices", map[string]string{"name": "stolen"}, token, true, 403)
	request("POST", "/api/v1/devices/"+device.ID+"/reauthorize", map[string]string{"replacement_token": strings.Repeat("b", 64)}, token, true, 403)
	replacement := strings.Repeat("a", 64)
	rotate := map[string]any{"operation_id": "retry-rotation", "expected_generation": 1, "replacement_token": replacement}
	reply := request("POST", "/api/v1/devices/"+device.ID+"/rotate", rotate, token, false, 200)
	raw, _ := json.Marshal(reply)
	if bytes.Contains(raw, []byte(replacement)) || bytes.Contains(raw, []byte("token_hash")) {
		t.Fatal("rotation response contains secret material")
	}
	request("POST", "/api/v1/devices/"+device.ID+"/rotate", rotate, token, false, 200)
	request("POST", "/api/v1/devices/"+device.ID+"/rotate", rotate, replacement, false, 200)
	res.LockVault()
	caps := request("GET", "/api/v1/mobile/capabilities", nil, replacement, false, 200)
	if string(caps["vault_unlocked"]) != "false" || string(caps["instance_id"]) != string(first["instance_id"]) {
		t.Fatal("locked capabilities or identity")
	}
	request("POST", "/api/v1/devices/"+device.ID+"/reauthorize", map[string]string{"replacement_token": strings.Repeat("b", 64)}, "", true, 401)
	request("POST", "/api/v1/devices/"+device.ID+"/revoke", nil, replacement, false, 200)
	request("GET", "/api/v1/mobile/capabilities", nil, replacement, false, 401)
	request("GET", "/api/v1/mobile/capabilities", nil, token, false, 401)
	if err := res.Vault.Unlock([]byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	renewed := request("POST", "/api/v1/devices/"+device.ID+"/reauthorize", map[string]any{"replacement_token": strings.Repeat("b", 64), "scopes": []string{}}, "", true, 200)
	var reauthorized users.Device
	json.Unmarshal(renewed["device"], &reauthorized)
	if reauthorized.ID != device.ID || reauthorized.Scopes == nil || len(reauthorized.Scopes) != 0 {
		t.Fatal("reauthorization identity/grants")
	}
}

func TestMobileIdentityLeavesParentRoutesAndNeverEchoesRotationSecret(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	d, token, err := us.CreateDevice(u.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	for _, path := range []string{"/api/library", "/api/v1/photos", "/api/v1/backups/uploads", "/api/unlock"} {
		if h.tryMobileIdentity(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil)) {
			t.Fatal("intercepted parent route", path)
		}
	}
	secret := strings.Repeat("a", 64)
	body := `{"operation_id":"test","expected_generation":10,"replacement_token":"` + secret + `"}`
	r := httptest.NewRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Weazl-Desk", "1")
	w := httptest.NewRecorder()
	h.tryMobileIdentity(w, r)
	if w.Code != 409 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("conflict response/code")
	}
}

func TestMobileRouterDispatchPreservesScopeAndLifecycleAdmission(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := us.CreateScopedDevice(u.ID, "reader", []string{users.PhotosRead, users.FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	if h.mobileParts == nil {
		t.Fatal("parts manager not initialized")
	}
	for _, route := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/.well-known/weazlcloud", 200},
		{"GET", "/api/v1/mobile/capabilities", 200},
		{"POST", "/api/v1/photos/source-collections", 403},
		{"POST", "/api/v1/backups/sources", 403},
		{"POST", "/api/v1/photos/uploads", 403},
		{"POST", "/api/capsules", 403},
		{"GET", "/api/v1/grabs/operations", 403},
		{"GET", "/api/uploads", 403},
	} {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("Idempotency-Key", "router-test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != route.want {
			t.Fatalf("%s %s status %d want %d", route.method, route.path, w.Code, route.want)
		}
	}
	if err := h.registry.Block(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v1/mobile/capabilities", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("identity bypassed owner drain", w.Code)
	}
}
