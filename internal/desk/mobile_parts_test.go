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
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobilePartsHTTPAutonomousHiddenOriginalAndRevoke(t *testing.T) {
	if _, e := exec.LookPath("restic"); e != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	us, e := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if e != nil {
		t.Fatal(e)
	}
	u, e := us.Create("parts", "parts-password", true)
	if e != nil {
		t.Fatal(e)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(u)
	if e = res.Vault.Forge([]byte("parts-vault"), []byte("parts-vault")); e != nil {
		t.Fatal(e)
	}
	defer res.LockVault()
	device, token, e := us.CreateScopedDevice(u.ID, "phone", []string{users.PhotosRead, users.PhotosWrite})
	if e != nil {
		t.Fatal(e)
	}
	_, wrong, e := us.CreateScopedDevice(u.ID, "other", []string{users.PhotosRead, users.PhotosWrite})
	if e != nil {
		t.Fatal(e)
	}
	body := []byte("unsupported camera original remains verified and hidden")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	request := func(method, path, credential string, raw []byte, want int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("X-Weazl-SHA256", hash)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]json.RawMessage{}
		if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	spec := map[string]any{"transport": "parts-v1", "commit_when_complete": true, "device_asset_id": "one", "filename": "camera.raw", "size": len(body), "sha256": hash, "hidden": true, "original_mode": "opaque-original-v1"}
	raw, _ := json.Marshal(spec)
	created := request("POST", "/api/v1/photos/uploads", token, raw, 201)
	var v mobileparts.View
	if e = json.Unmarshal(created["transfer"], &v); e != nil {
		t.Fatal(e)
	}
	root := "/api/v1/photos/uploads/" + v.ID
	request("GET", root, wrong, nil, 404)
	request("PUT", root+"/components/original/parts/0", token, body, 200)
	request("PUT", root+"/components/original/parts/0", token, body, 200)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.runMobileParts(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(20 * time.Second)
	// Observe internal state: worker completion never depends on client polling.
	for time.Now().Before(deadline) {
		v, e = h.mobileParts.Status(res, v.ID, device.ID)
		if e != nil {
			t.Fatal(e)
		}
		if v.Status == "stored" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v.Status != "stored" {
		t.Fatalf("autonomous finalize: %+v", v)
	}
	page, e := res.Lib.PhotoPage(context.Background(), 20, "", "")
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("hidden leaked: %+v %v", page, e)
	}
	// Hidden is a visibility scope inside the already-unlocked owner vault.
	// The same device credential can browse and read it without another unlock.
	hiddenPage := request("GET", "/api/v1/photos?mode=hidden", token, nil, 200)
	var items []struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(hiddenPage["items"], &items); e != nil || len(items) != 1 {
		t.Fatalf("hidden upload missing from Hidden: %s %v", hiddenPage["items"], e)
	}
	originalPath := "/api/v1/photos/assets/" + items[0].ID + "/original"
	normalRequest := httptest.NewRequest("GET", originalPath, nil)
	normalRequest.Header.Set("Authorization", "Bearer "+token)
	normalResponse := httptest.NewRecorder()
	h.ServeHTTP(normalResponse, normalRequest)
	if normalResponse.Code != 404 {
		t.Fatalf("hidden original leaked into normal context: status=%d", normalResponse.Code)
	}
	originalRequest := httptest.NewRequest("GET", originalPath+"?hidden=1", nil)
	originalRequest.Header.Set("Authorization", "Bearer "+token)
	originalResponse := httptest.NewRecorder()
	h.ServeHTTP(originalResponse, originalRequest)
	if originalResponse.Code != 200 || !bytes.Equal(originalResponse.Body.Bytes(), body) {
		t.Fatalf("hidden original with existing credential: status=%d", originalResponse.Code)
	}
	retry := request("POST", "/api/v1/photos/uploads", token, raw, 200)
	if len(retry["receipt"]) == 0 {
		t.Fatal("lost reply receipt not recovered")
	}
	// A different body under the same source identity never creates another asset.
	spec["hidden"] = false
	changed, _ := json.Marshal(spec)
	request("POST", "/api/v1/photos/uploads", token, changed, 409)
	if e = us.RevokeDevice(u.ID, device.ID); e != nil {
		t.Fatal(e)
	}
	request("GET", root, token, nil, 401)
}
