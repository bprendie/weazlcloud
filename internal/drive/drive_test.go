package drive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestDriveIsNotUnlock(t *testing.T) {
	s := httptest.NewServer(New())
	t.Cleanup(s.Close)
	post, _ := http.NewRequest(http.MethodPost, s.URL+"/unlock", nil)
	res, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("unlock %d %q", res.StatusCode, body)
	}
	res, err = http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("root %d", res.StatusCode)
	}
	if strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("root leaked desk %q", body)
	}
	res, err = http.Get(s.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Fatalf("ready %q", body)
	}
}

func TestMultiDriveAuthenticatesAndServesWebDAV(t *testing.T) {
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(store.VaultPath(u), store.NodeKeyPath(u))
	if err := v.Forge([]byte("vault-pass"), []byte("vault-pass")); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(NewMulti(store))
	t.Cleanup(s.Close)
	options, _ := http.NewRequest(http.MethodOptions, s.URL+"/", nil)
	optionsRes, err := http.DefaultClient.Do(options)
	if err != nil || optionsRes.StatusCode != http.StatusOK || optionsRes.Header.Get("DAV") == "" {
		t.Fatalf("OPTIONS status=%v err=%v dav=%q", optionsRes.StatusCode, err, optionsRes.Header.Get("DAV"))
	}
	optionsRes.Body.Close()
	req, _ := http.NewRequest("PROPFIND", s.URL+"/", nil)
	req.SetBasicAuth("alice", "alice-password")
	req.Header.Set("Depth", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND %d", res.StatusCode)
	}
	if !strings.Contains(string(body), "<D:href>.</D:href>") {
		t.Fatalf("root href was not normalized: %s", body)
	}
	unauth, _ := http.Get(s.URL + "/")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated %d", unauth.StatusCode)
	}
	if err := store.SetDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	disabled, _ := http.NewRequest(http.MethodGet, s.URL+"/", nil)
	disabled.SetBasicAuth("alice", "alice-password")
	disabledRes, err := http.DefaultClient.Do(disabled)
	if err != nil {
		t.Fatal(err)
	}
	disabledRes.Body.Close()
	if disabledRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled WebDAV account status=%d", disabledRes.StatusCode)
	}
}
