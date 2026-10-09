package desk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileStatusBatchIsolationLimitsAndCapabilities(t *testing.T) {
	dir := t.TempDir()
	us, e := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if e != nil {
		t.Fatal(e)
	}
	u, e := us.Create("batch-owner", "test-password", true)
	if e != nil {
		t.Fatal(e)
	}
	d, token, e := us.CreateScopedDevice(u.ID, "phone", []string{users.PhotosWrite, users.PhotosRead})
	if e != nil {
		t.Fatal(e)
	}
	other, wrong, e := us.CreateScopedDevice(u.ID, "other", []string{users.PhotosWrite})
	if e != nil {
		t.Fatal(e)
	}
	_, readonly, e := us.CreateScopedDevice(u.ID, "read", []string{users.PhotosRead})
	if e != nil {
		t.Fatal(e)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(u)
	if e = res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); e != nil {
		t.Fatal(e)
	}
	defer res.LockVault()
	body := []byte("hidden live photo")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	spec := mobileparts.Spec{Kind: "photo", DeviceID: d.ID, CommitWhenComplete: true, Components: []mobileparts.Component{{ID: "original", Size: int64(len(body)), SHA256: hash}, {ID: "motion", Size: int64(len(body)), SHA256: hash}}, Payload: json.RawMessage(`{"hidden":true}`)}
	id := strings.Repeat("a", 32)
	if _, e = h.mobileParts.Create(res, u.ID, id, spec); e != nil {
		t.Fatal(e)
	}
	request := func(method, credential string, in any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest(method, "/api/v1/photos/uploads/status", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Weazl-Desk", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		return w
	}
	input := map[string]any{"upload_ids": []string{id, strings.Repeat("f", 32)}}
	w := request("POST", token, input, 200)
	if w.Header().Get("Retry-After") != "5" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("pending headers", w.Header())
	}
	var out struct {
		Items []struct {
			UploadID string           `json:"upload_id"`
			Code     string           `json:"code"`
			Transfer mobileparts.View `json:"transfer"`
		} `json:"items"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Items) != 2 || out.Items[0].Transfer.Status != "uploading" || out.Items[0].Transfer.ID != id || out.Items[1].Code != "not_found" {
		t.Fatal("ordered projection", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("payload")) || bytes.Contains(w.Body.Bytes(), []byte("hidden")) || bytes.Contains(w.Body.Bytes(), []byte("key")) {
		t.Fatal("private session leaked")
	}
	w = request("POST", wrong, input, 200)
	if bytes.Contains(w.Body.Bytes(), []byte("transfer")) {
		t.Fatal("foreign device read")
	}
	request("POST", readonly, input, 403)
	request("GET", token, input, 405)
	request("POST", token, map[string]any{"upload_ids": []string{id, id}}, 400)
	request("POST", token, map[string]any{"upload_ids": []string{}}, 400)
	request("POST", token, map[string]any{"upload_ids": []string{"bad"}}, 400)
	many := make([]string, 101)
	for i := range many {
		many[i] = id
	}
	request("POST", token, map[string]any{"upload_ids": many}, 400)
	request("POST", token, map[string]any{"upload_ids": []string{id}, "unknown": true}, 400)
	spec.DeviceID = other.ID
	spec.Kind = "file"
	spec.Components = spec.Components[:1]
	foreignID := strings.Repeat("b", 32)
	if _, e = h.mobileParts.Create(res, u.ID, foreignID, spec); e != nil {
		t.Fatal(e)
	}
	request("POST", wrong, map[string]any{"upload_ids": []string{foreignID}}, 200)
	capReq := httptest.NewRequest("GET", "/api/v1/mobile/capabilities", nil)
	capReq.Header.Set("Authorization", "Bearer "+token)
	capW := httptest.NewRecorder()
	h.ServeHTTP(capW, capReq)
	var caps struct {
		Features map[string]bool `json:"features"`
		Limits   map[string]int  `json:"limits"`
	}
	if e = json.Unmarshal(capW.Body.Bytes(), &caps); e != nil {
		t.Fatal(e)
	}
	if !caps.Features["upload_status_batch_v1"] || caps.Limits["upload_status_batch_items"] != 100 || caps.Limits["upload_status_poll_seconds"] != 5 {
		t.Fatal("capability absent", capW.Body.String())
	}
	if e = h.mobileParts.Cancel(res, id, d.ID); e != nil {
		t.Fatal(e)
	}
	w = request("POST", token, input, 200)
	if w.Header().Get("Retry-After") != "" {
		t.Fatal("terminal response requests polling")
	}
	res.LockVault()
	request("POST", token, input, 423)
}
func TestReceiveResourceDefaults(t *testing.T) {
	small := resolveMobileReceiveLimits(2, 4<<30)
	large := resolveMobileReceiveLimits(16, 128<<30)
	if small.PerOwner != 2 || small.Global != 4 || small.PendingBytes != 1<<30 {
		t.Fatal(small)
	}
	if large.PerOwner != 8 || large.Global != 16 || large.PendingBytes != 32<<30 {
		t.Fatal(large)
	}
}
