package desk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
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
