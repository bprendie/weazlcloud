package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bprendie/weazlcloud/internal/config"
)

func TestPhase1ConcurrentDeskAndWebDAVWritesShareCatalog(t *testing.T) {
	n, err := Start(config.Config{DataDir: filepath.Join(t.TempDir(), "data"), DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	postClient := func(client *http.Client, path string, body any) *http.Response {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, "http://"+n.DeskAddr()+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Weazl-Desk", "1")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	post := func(path string, body any) *http.Response { return postClient(c, path, body) }
	res := post("/api/bootstrap", map[string]string{"username": "alice", "password": "alice-password", "vault_passphrase": "alice-vault", "confirm": "alice-vault"})
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("bootstrap %d: %s", res.StatusCode, body)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "alice-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/users", map[string]string{"username": "bob", "password": "bob-password", "vault_passphrase": "bob-vault", "confirm": "bob-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create bob %d", res.StatusCode)
	}
	res.Body.Close()
	bobJar, _ := cookiejar.New(nil)
	bobClient := &http.Client{Jar: bobJar}
	res = postClient(bobClient, "/api/login", map[string]string{"username": "bob", "password": "bob-password"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob login %d", res.StatusCode)
	}
	res.Body.Close()
	res = postClient(bobClient, "/api/unlock", map[string]string{"passphrase": "bob-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob unlock %d", res.StatusCode)
	}
	res.Body.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPut, "http://"+n.DeskAddr()+"/api/library?path=desk.txt", bytes.NewReader([]byte("desk")))
		req.Header.Set("X-Weazl-Desk", "1")
		res, err := c.Do(req)
		if err != nil {
			errs <- err
			return
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			errs <- &statusError{got: res.StatusCode}
		}
	}()
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPut, "http://"+n.DeskAddr()+"/api/library?path=bob.txt", bytes.NewReader([]byte("bob")))
		req.Header.Set("X-Weazl-Desk", "1")
		res, err := bobClient.Do(req)
		if err != nil {
			errs <- err
			return
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			errs <- &statusError{got: res.StatusCode}
		}
	}()
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPut, "http://"+n.DriveAddr()+"/dav.txt", bytes.NewReader([]byte("dav")))
		req.SetBasicAuth("alice", "alice-password")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			errs <- err
			return
		}
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			errs <- &statusError{got: res.StatusCode}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	res, err = c.Get("http://" + n.DeskAddr() + "/api/library")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range out.Files {
		seen[f.Path] = true
	}
	if !seen["desk.txt"] || !seen["dav.txt"] {
		t.Fatalf("concurrent writes lost a catalog entry: %#v", seen)
	}
}

type statusError struct{ got int }

func (e *statusError) Error() string { return "unexpected HTTP status" }
