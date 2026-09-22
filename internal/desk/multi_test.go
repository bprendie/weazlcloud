package desk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMultiuserBootstrapLoginUnlockAndIsolation(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "capsules")), quota.New(dir), "", "", dir)
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path string, body any) *http.Response {
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(http.MethodPost, s.URL+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Weazl-Desk", "1")
		res, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post("/api/bootstrap", map[string]string{"username": "alice", "password": "alice-password", "vault_passphrase": "alice-vault", "confirm": "alice-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "alice-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/users", map[string]string{"username": "bob", "password": "bob-password", "vault_passphrase": "bob-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/logout", map[string]any{})
	res.Body.Close()
	res = post("/api/login", map[string]string{"username": "bob", "password": "bob-password"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "alice-vault"})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-user unlock: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "bob-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob unlock: %d", res.StatusCode)
	}
	res.Body.Close()
	res, err = c.Get(s.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", res.StatusCode)
	}
}

func TestAdminCanManageAccountsWithoutVaultDataAndCannotRemoveLastAdmin(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := us.Create("admin", "admin-password", true)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := us.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _ := us.Login(admin)
	aliceToken, _ := us.Login(alice)
	h := NewMulti(us, capsule.New(filepath.Join(dir, "capsules")), quota.New(dir), "", "", dir)
	get := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
		r.AddCookie(&http.Cookie{Name: "weazl_session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := get(aliceToken); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin listed accounts: %d", got.Code)
	}
	listing := get(adminToken)
	if listing.Code != http.StatusOK || bytes.Contains(bytes.ToLower(listing.Body.Bytes()), []byte("vault")) {
		t.Fatalf("admin list exposed vault information: %d %s", listing.Code, listing.Body.String())
	}
	post := func(path string, token string, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "weazl_session", Value: token})
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := post("/api/admin/users/disable", adminToken, `{"id":"`+admin.ID+`","disabled":true}`); got.Code != http.StatusConflict {
		t.Fatalf("last admin disable status=%d body=%s", got.Code, got.Body.String())
	}
	if got := post("/api/admin/users/disable", adminToken, `{"id":"`+alice.ID+`","disabled":true}`); got.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", got.Code, got.Body.String())
	}
	deniedReq := httptest.NewRequest(http.MethodGet, "/api/uploads", nil)
	deniedReq.AddCookie(&http.Cookie{Name: "weazl_session", Value: aliceToken})
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("disabled upload session status=%d", denied.Code)
	}
	if got := post("/api/admin/users/delete", adminToken, `{"id":"`+alice.ID+`","confirm_username":"alice"}`); got.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", got.Code, got.Body.String())
	}
	if _, exists := us.User(alice.ID); exists {
		t.Fatal("account was not deleted")
	}
}
