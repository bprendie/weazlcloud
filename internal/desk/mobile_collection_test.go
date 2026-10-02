package desk

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMobileCollectionsDispatcherPagingAndLockedVault(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := us.Create("collections", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := us.Login(user)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	res := h.registry.For(user)
	defer res.LockVault()
	request := func(method, path string, body any, want int) map[string]json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
		r.Header.Set("X-Weazl-Desk", "1")
		w := httptest.NewRecorder()
		if !h.tryMobileCollections(w, r) {
			t.Fatal("route not dispatched")
		}
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private cache policy missing")
		}
		var value map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &value)
		return value
	}
	request("GET", "/api/v1/photos/collections", nil, 401)
	if err = res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"First", "Second"} {
		request("POST", "/api/v1/photos/collections", map[string]any{"action": "save-folder", "folder": map[string]string{"title": title}}, 200)
	}
	page := request("GET", "/api/v1/photos/collections?limit=1", nil, 200)
	var nodes []json.RawMessage
	_ = json.Unmarshal(page["nodes"], &nodes)
	if len(nodes) != 1 {
		t.Fatal("unbounded page")
	}
	var cursor string
	_ = json.Unmarshal(page["next"], &cursor)
	next := request("GET", "/api/v1/photos/collections?limit=1&cursor="+cursor, nil, 200)
	var checkpoint string
	_ = json.Unmarshal(next["checkpoint"], &checkpoint)
	if checkpoint == "" {
		t.Fatal("missing checkpoint")
	}
	request("GET", "/api/v1/photos/collections?hidden=1&cursor="+checkpoint, nil, 409)
	wrapped, _ := base64.RawURLEncoding.DecodeString(checkpoint)
	plain, e := res.Vault.Unwrap(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	var decoded mobileCollectionCursor
	if e = json.Unmarshal(plain, &decoded); e != nil {
		t.Fatal(e)
	}
	clear(plain)
	for _, mutate := range []func(*mobileCollectionCursor){func(c *mobileCollectionCursor) { c.Schema = 99 }, func(c *mobileCollectionCursor) { c.Schema = 0 }, func(c *mobileCollectionCursor) { c.Kind = "files" }, func(c *mobileCollectionCursor) { c.Mode = "future" }} {
		invalid := decoded
		mutate(&invalid)
		b, _ := json.Marshal(invalid)
		enc, e := res.Vault.Wrap(b)
		clear(b)
		if e != nil {
			t.Fatal(e)
		}
		request("GET", "/api/v1/photos/collections?cursor="+base64.RawURLEncoding.EncodeToString(enc), nil, 409)
	}
	request("POST", "/api/v1/photos/collections", map[string]any{"action": "save-folder", "folder": map[string]string{"title": "Third"}}, 200)
	request("GET", "/api/v1/photos/collections?cursor="+cursor, nil, 409)
	delta := request("GET", "/api/v1/photos/collections?cursor="+checkpoint, nil, 200)
	var changes []json.RawMessage
	_ = json.Unmarshal(delta["changes"], &changes)
	if len(changes) != 1 {
		t.Fatalf("delta: %s", delta["changes"])
	}
	request("GET", "/api/v1/photos/collections?limit=201", nil, 400)
	request("DELETE", "/api/v1/photos/collections", nil, 405)
	r := httptest.NewRequest("GET", "/api/v1/photos/other", nil)
	if h.tryMobileCollections(httptest.NewRecorder(), r) {
		t.Fatal("claimed foreign route")
	}
}
