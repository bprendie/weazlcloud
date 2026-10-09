package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestPhotoContentLookupOwnerScopeAndContract(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic required")
	}
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create("lookup-owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create("lookup-other", "test-password", false)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(store, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(owner)
	otherRes := h.registry.For(other)
	if err = res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
		t.Fatal(err)
	}
	if err = otherRes.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
		t.Fatal(err)
	}
	defer res.LockVault()
	defer otherRes.LockVault()
	data := []byte("owner original")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	_, err = res.Lib.Put(context.Background(), "Photos/a.jpg", data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := res.Lib.Metadata(context.Background(), "Photos/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := store.CreateScopedDevice(owner.ID, "reader", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := store.CreateScopedDevice(other.ID, "reader", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}
	_, writer, err := store.CreateScopedDevice(owner.ID, "writer", []string{users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"items": []map[string]any{{"sha256": hash, "size": len(data)}}})
	request := func(token string, raw []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/photos/lookup", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Weazl-Desk", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder) library.PhotoContentPage {
		t.Helper()
		var p library.PhotoContentPage
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	w := request(token, body, 200)
	p := decode(w)
	if w.Header().Get("Cache-Control") != "private, no-store" || !p.Results[0].Exists || p.Results[0].Matches[0].AssetID != f.EntryID {
		t.Fatalf("lookup result %+v expected=%q cache=%q", p, f.EntryID, w.Header().Get("Cache-Control"))
	}
	if p = decode(request(otherToken, body, 200)); p.Results[0].Exists {
		t.Fatal("another owner's content leaked")
	}
	request(writer, body, 403)
	request("invalid", body, 401)
	request(token, []byte(`{"items":[]}`), 400)
	request(token, append(append([]byte{}, body...), []byte(" {}")...), 400)
	request(token, []byte(`{"items":[{"sha256":"`+hash+`","size":0}]}`), 400)
	request(token, []byte(`{"items":[{"sha256":"`+strings.Repeat("g", 64)+`","size":1}]}`), 400)
	request(token, []byte(`{"items":[],"padding":"`+strings.Repeat("x", 65536)+`"}`), 400)
	if _, err = res.Lib.SetPhotoFolderHidden(context.Background(), "Photos", true); err != nil {
		t.Fatal(err)
	}
	if decode(request(token, body, 200)).Results[0].Exists {
		t.Fatal("default lookup exposed Hidden")
	}
	hidden, _ := json.Marshal(map[string]any{"items": []map[string]any{{"sha256": hash, "size": len(data)}}, "include_hidden": true})
	p = decode(request(token, hidden, 200))
	if !p.Results[0].Exists || !p.Results[0].Matches[0].Hidden {
		t.Fatal("explicit Hidden lookup lost identity")
	}
	res.LockVault()
	request(token, body, 423)
	if err = store.RevokeDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	request(token, body, 401)
}
