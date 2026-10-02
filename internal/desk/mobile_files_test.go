package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileFilesHTTPStableReadsSyncAndDeviceIsolation(t *testing.T) {
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
	device, token, err := us.CreateScopedDevice(user.ID, "phone", []string{users.FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := us.CreateScopedDevice(user.ID, "other", []string{users.FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	_, photoToken, err := us.CreateDevice(user.ID, "legacy photos")
	if err != nil {
		t.Fatal(err)
	}
	f, err := res.Lib.Put(context.Background(), "Docs/a.txt", []byte("abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	f, err = res.Lib.Metadata(context.Background(), "Docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/v1/files/" + f.EntryID
	request := func(method, url, credential, body string, headers map[string]string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Weazl-Desk", "1")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		if !h.tryMobileFiles(w, r) {
			t.Fatal("route not recognized")
		}
		if w.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, w.Code, want, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("missing private no-store")
		}
		return w
	}
	metadata := request("GET", route, token, "", nil, 200)
	if strings.Contains(metadata.Body.String(), "reference") || strings.Contains(metadata.Body.String(), "hash") {
		t.Fatal("storage identity leaked")
	}
	etag := metadata.Header().Get("ETag")
	head := request("HEAD", route+"/content", token, "", nil, 200)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != "10" || head.Header().Get("ETag") != etag {
		t.Fatal("HEAD mismatch")
	}
	partial := request("GET", route+"/content", token, "", map[string]string{"Range": "bytes=2-5", "If-Range": etag}, 206)
	if partial.Body.String() != "cdef" {
		t.Fatal("range bytes")
	}
	request("GET", route+"/content", token, "", map[string]string{"Range": "bytes=0-1,3-4"}, 416)
	request("GET", route+"/content", token, "", map[string]string{"If-Match": "\"old\""}, 412)
	request("GET", route+"/content", token, "", map[string]string{"If-None-Match": "W/" + etag}, 304)
	request("GET", route, photoToken, "", nil, 403)
	foreign, err := us.Create("foreign", "foreign-password", false)
	if err != nil {
		t.Fatal(err)
	}
	foreignRes := h.registry.For(foreign)
	if err := foreignRes.Vault.Forge([]byte("foreign-vault"), []byte("foreign-vault")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { foreignRes.LockVault() })
	_, foreignToken, err := us.CreateScopedDevice(foreign.ID, "foreign phone", []string{users.FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	request("GET", route, foreignToken, "", nil, 404)
	request("GET", "/api/v1/files/00000000000000000000000000000000", token, "", nil, 404)
	if _, err := res.Lib.Put(context.Background(), "Docs/a.txt", []byte("new revision")); err != nil {
		t.Fatal(err)
	}
	full := request("GET", route+"/content", token, "", map[string]string{"Range": "bytes=2-5", "If-Range": etag}, 200)
	if full.Body.String() != "new revision" || full.Header().Get("ETag") == etag {
		t.Fatal("old ETag mixed revisions")
	}
	snapshot := request("GET", "/api/v1/files/sync?limit=200", token, "", nil, 200)
	var page library.MobileFilesPage
	if err := json.Unmarshal(snapshot.Body.Bytes(), &page); err != nil || page.Checkpoint == "" {
		t.Fatalf("snapshot: %v %+v", err, page)
	}
	request("GET", "/api/v1/files/sync?cursor="+page.Checkpoint, otherToken, "", nil, 409)
	request("GET", "/api/v1/files/sync?device_id=foreign", token, "", nil, 403)
	body, _ := json.Marshal(map[string]string{"checkpoint": page.Checkpoint, "device_id": "foreign"})
	request("POST", "/api/v1/files/sync/checkpoint", token, string(body), nil, 403)
	body, _ = json.Marshal(map[string]string{"checkpoint": page.Checkpoint, "device_id": device.ID})
	request("POST", "/api/v1/files/sync/checkpoint", token, string(body), nil, 200)
	request("GET", "/api/v1/files/sync?resume=1", token, "", nil, 200)
	if err := res.Lib.Delete("Docs/a.txt"); err != nil {
		t.Fatal(err)
	}
	request("GET", route+"/content", token, "", nil, 404)
	if _, err := res.Lib.Put(context.Background(), "Docs/a.txt", []byte("reuse")); err != nil {
		t.Fatal(err)
	}
	request("GET", route, token, "", nil, 404)
	res.LockVault()
	request("GET", route, token, "", nil, 401)
}

func TestMobileFilesRevokedWriterEmitsNoMoreBytes(t *testing.T) {
	var out bytes.Buffer
	revoked := false
	w := mobileFilesAuthorizedWriter{&out, func() error {
		if revoked {
			return users.ErrNoSession
		}
		return nil
	}}
	if _, err := w.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if n, err := w.Write([]byte("secret")); n != 0 || err != users.ErrNoSession || out.String() != "before" {
		t.Fatalf("n=%d err=%v bytes=%q", n, err, out.String())
	}
}

func TestMobileFilesPreconditionsAndOwnerETags(t *testing.T) {
	modified := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		headers map[string]string
		want    int
	}{
		{map[string]string{"If-Match": "\"other\", \"tag\""}, 0},
		{map[string]string{"If-Match": "W/\"tag\""}, 412},
		{map[string]string{"If-None-Match": "\"other\", W/\"tag\""}, 304},
		{map[string]string{"If-None-Match": "*"}, 304},
		{map[string]string{"If-Unmodified-Since": modified.Add(-time.Hour).Format(http.TimeFormat)}, 412},
		{map[string]string{"If-Modified-Since": modified.Format(http.TimeFormat)}, 304},
		{map[string]string{"If-None-Match": "\"other\"", "If-Modified-Since": modified.Format(http.TimeFormat)}, 0},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		for k, v := range tc.headers {
			r.Header.Set(k, v)
		}
		if got := mobileFilesPrecondition(r, `"tag"`, modified); got != tc.want {
			t.Fatalf("headers=%v got=%d want=%d", tc.headers, got, tc.want)
		}
	}
	if mobileFilesIfRange(`W/"tag"`, `"tag"`, modified) || !mobileFilesIfRange(modified.Format(http.TimeFormat), `"tag"`, modified) {
		t.Fatal("If-Range validators")
	}
	item := library.MobileFileItem{ID: "stable-id", Revision: 1}
	if mobileFilesETag("alice", item) == mobileFilesETag("bob", item) {
		t.Fatal("ETag lacks owner scope")
	}
}
