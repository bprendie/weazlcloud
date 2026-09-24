package desk

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestTakeoutOwnerJobAndIsolation(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	w, err := z.Create("Takeout/Google Photos/Photos from 2020/pic.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("photo")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "part.zip"), archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(root, "capsules")), quota.New(root), "", "", root)
	h.EnableTakeout(stage, "alice")
	s := httptest.NewServer(h)
	defer s.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(url string, body any) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, s.URL+url, bytes.NewReader(raw))
		req.Header.Set("X-Weazl-Desk", "1")
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post("/api/bootstrap", map[string]string{"username": "alice", "password": "alice-pass", "vault_passphrase": "alice-vault", "confirm": "alice-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "alice-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/takeout", map[string]string{"name": "part.zip"})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("start %d", res.StatusCode)
	}
	res.Body.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, err = c.Get(s.URL + "/api/takeout")
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Jobs []struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"jobs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if len(body.Jobs) == 1 && body.Jobs[0].Status == "complete" {
			break
		}
		if len(body.Jobs) == 1 && body.Jobs[0].Status == "failed" {
			t.Fatal(body.Jobs[0].Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("import did not complete")
		}
		time.Sleep(50 * time.Millisecond)
	}
	res, err = c.Get(s.URL + "/api/library?path=" + "Google%20Takeout%2FPhotos%2FPhotos%20from%202020%2Fpic.jpg")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("imported photo %d", res.StatusCode)
	}
	res = post("/api/users", map[string]string{"username": "bob", "password": "bob-pass", "vault_passphrase": "bob-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create bob %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/logout", map[string]any{})
	res.Body.Close()
	res = post("/api/login", map[string]string{"username": "bob", "password": "bob-pass"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob login %d", res.StatusCode)
	}
	res.Body.Close()
	res, err = c.Get(s.URL + "/api/takeout")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("other user saw staging: %d", res.StatusCode)
	}
	res = post("/api/takeout", map[string]string{"name": "part.zip"})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("other user started import: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(stage, "part.zip")); err != nil || strings.Contains(errString(err), "not exist") {
		t.Fatal("staged ZIP removed")
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
