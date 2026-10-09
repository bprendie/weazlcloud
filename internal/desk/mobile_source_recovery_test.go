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
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileSourceRecoveryOwnerScopeAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := us.Create("recovery-owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := us.Create("other-owner", "test-password", false)
	if err != nil {
		t.Fatal(err)
	}
	old, oldToken, err := us.CreateScopedDevice(owner.ID, "old phone", []string{users.PhotosRead, users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	current, token, err := us.CreateScopedDevice(owner.ID, "new phone", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}
	_, wrongScope, err := us.CreateScopedDevice(owner.ID, "files", []string{users.FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	_, foreignToken, err := us.CreateScopedDevice(foreign.ID, "other phone", []string{users.PhotosRead})
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
	if err = res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
		t.Fatal(err)
	}
	if err = other.Vault.Forge([]byte("other-vault"), []byte("other-vault")); err != nil {
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
	ops := []catalog.SourceOperation{
		{OperationID: "folder", Namespace: "photokit-old", SourceID: "opaque/folder", SourceRevision: "f1", Kind: "folder", Title: "Kept folder"},
		{OperationID: "album", Namespace: "photokit-old", SourceID: "opaque/album", SourceRevision: "a1", Kind: "album", Title: "Kept album", ParentSourceID: "opaque/folder"},
	}
	out, err := res.Lib.ImportPhotoSources(context.Background(), old.ID, ops, false)
	if err != nil || len(out) != 2 || out[1].Status != "applied" {
		t.Fatalf("setup: %+v %v", out, err)
	}
	newOp := ops[1]
	newOp.Namespace, newOp.ParentSourceID = "photokit-current", ""
	if _, err = res.Lib.ImportPhotoSources(context.Background(), current.ID, []catalog.SourceOperation{newOp}, false); err != nil {
		t.Fatal(err)
	}
	if err = us.RevokeDevice(owner.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	call := func(credential string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		if s, ok := body.(string); ok {
			raw = []byte(s)
		}
		r := httptest.NewRequest("POST", "/api/v1/photos/source-collections/lookup", bytes.NewReader(raw))
		r.Header.Set("X-Weazl-Desk", "1")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		// A foreign/invalid bearer must not fall back to this valid owner's cookie.
		r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		return w
	}
	input := map[string]any{"source_ids": []string{"opaque/album", "opaque/folder", "missing"}, "limit": 1}
	seen := map[string]bool{}
	var firstCursor string
	for {
		w := call(token, input, 200)
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private cache policy missing")
		}
		var page catalog.SourceRecoveryPage
		if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Matches) != 1 {
			t.Fatal(page, err)
		}
		m := page.Matches[0]
		if seen[m.ServerID] || !m.TargetExists || m.CurrentServerRevision == nil || *m.CurrentServerRevision != 1 || m.SourceRevision == "" || m.ServerRevision != 1 {
			t.Fatal("bad recovery result", m)
		}
		seen[m.ServerID] = true
		if !page.HasMore {
			break
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
		}
		input["cursor"] = page.NextCursor
	}
	if len(seen) != 3 || !seen[out[0].ServerID] || !seen[out[1].ServerID] {
		t.Fatal("old device namespace lost", seen)
	}
	// Filters and owners are bound into the authenticated encrypted cursor.
	input["cursor"] = firstCursor
	call(foreignToken, input, 400)
	input["namespace"] = "photokit-current"
	call(token, input, 400)
	delete(input, "cursor")
	delete(input, "namespace")
	w := call(foreignToken, input, 200)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"matches":[]`)) {
		t.Fatal("another owner's source mappings leaked", w.Body.String())
	}
	call("", input, 200)
	call(wrongScope, input, 403)
	call(oldToken, input, 401)
	call(strings.Repeat("f", 64), input, 401)
	for _, invalid := range []string{
		`{}`, `{"source_ids":[]}`, `{"source_ids":["a","a"]}`,
		`{"source_ids":["a"],"owner_id":"other"}`, `{"source_ids":["a"],"device_id":"old"}`,
		`{"source_ids":["a"],"limit":201}`, `{"source_ids":["a"],"limit":0}`,
		`{"source_ids":["a"],"cursor":"bad"}`, `{"source_ids":["a"]} {}`,
	} {
		call(token, invalid, 400)
	}
	many := make([]string, 201)
	call(token, map[string]any{"source_ids": many}, 400)
	call(token, map[string]any{"source_ids": []string{strings.Repeat("x", 4097)}}, 400)
	capReq := httptest.NewRequest("GET", "/api/v1/mobile/capabilities", nil)
	capReq.Header.Set("Authorization", "Bearer "+token)
	capW := httptest.NewRecorder()
	h.ServeHTTP(capW, capReq)
	if capW.Code != 200 || !bytes.Contains(capW.Body.Bytes(), []byte(`"source_collection_recovery":true`)) || !bytes.Contains(capW.Body.Bytes(), []byte(`"source_collection_lookup_matches":200`)) {
		t.Fatal("missing capability", capW.Body.String())
	}
	res.LockVault()
	call(token, input, 423)
}
