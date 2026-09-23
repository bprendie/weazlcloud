package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bprendie/weazlcloud/internal/config"
)

func TestPhase1ConcurrentDeskAndWebDAVWritesShareCatalog(t *testing.T) {
	n, err := Start(config.Config{DataDir: filepath.Join(t.TempDir(), "data"), DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0", StorageBackend: "shared-experimental"})
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
	get, err := http.NewRequest(http.MethodGet, "http://"+n.DriveAddr()+"/dav.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	get.SetBasicAuth("alice", "alice-password")
	download, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	davBytes, readErr := io.ReadAll(download.Body)
	download.Body.Close()
	if readErr != nil || download.StatusCode != http.StatusOK || string(davBytes) != "dav" {
		t.Fatalf("shared WebDAV download status=%d body=%q err=%v", download.StatusCode, davBytes, readErr)
	}
	sharedPayload := []byte("same bytes owned independently by two people")
	for _, client := range []*http.Client{c, bobClient} {
		put, _ := http.NewRequest(http.MethodPut, "http://"+n.DeskAddr()+"/api/library?path=private-name.txt", bytes.NewReader(sharedPayload))
		put.Header.Set("X-Weazl-Desk", "1")
		response, putErr := client.Do(put)
		if putErr != nil {
			t.Fatal(putErr)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("shared duplicate upload status=%d", response.StatusCode)
		}
	}
	type aggregate struct {
		Scope     string `json:"dedupe_scope"`
		Percent   int    `json:"dedupe_percent"`
		Logical   int64  `json:"logical_bytes"`
		Unique    int64  `json:"unique_bytes"`
		Allocated int64  `json:"shared_allocated_bytes"`
	}
	quotaFor := func(client *http.Client) (aggregate, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, "http://"+n.DeskAddr()+"/api/quota", nil)
		request.Header.Set("X-Weazl-Desk", "1")
		response, getErr := client.Do(request)
		if getErr != nil {
			t.Fatal(getErr)
		}
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("quota status=%d err=%v", response.StatusCode, readErr)
		}
		var result aggregate
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result, string(body)
	}
	adminStats, adminJSON := quotaFor(c)
	userStats, userJSON := quotaFor(bobClient)
	if adminStats != userStats || adminStats.Logical <= adminStats.Unique || adminStats.Percent == 0 || adminStats.Allocated <= 0 {
		t.Fatalf("global quota aggregation differs by role: admin=%+v user=%+v", adminStats, userStats)
	}
	if adminStats.Scope != "all live and Trash references using shared storage" || strings.Contains(adminJSON, "private-name.txt") || strings.Contains(userJSON, "private-name.txt") {
		t.Fatalf("quota response exposed ownership detail or omitted scope: %s", adminJSON)
	}
}

func TestPhase8ConcurrentReplacementsPublishWholeSuccessfulPayload(t *testing.T) {
	n, err := Start(config.Config{DataDir: filepath.Join(t.TempDir(), "data"), DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path string, body any) *http.Response {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, "http://"+n.DeskAddr()+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Weazl-Desk", "1")
		res, requestErr := c.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return res
	}
	res := post("/api/bootstrap", map[string]string{"username": "replace", "password": "replace-password", "vault_passphrase": "replace-vault", "confirm": "replace-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "replace-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock status=%d", res.StatusCode)
	}
	res.Body.Close()

	one := bytes.Repeat([]byte("desk-payload-"), 4096)
	two := bytes.Repeat([]byte("webdav-payload-"), 4096)
	requests := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPut, "http://"+n.DeskAddr()+"/api/library?path=race.bin", bytes.NewReader(one))
		req.Header.Set("X-Weazl-Desk", "1")
		response, requestErr := c.Do(req)
		if requestErr != nil {
			requests <- requestErr
			return
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			requests <- &statusError{got: response.StatusCode}
		}
	}()
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPut, "http://"+n.DriveAddr()+"/race.bin", bytes.NewReader(two))
		req.SetBasicAuth("replace", "replace-password")
		response, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			requests <- requestErr
			return
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			requests <- &statusError{got: response.StatusCode}
		}
	}()
	wg.Wait()
	close(requests)
	for requestErr := range requests {
		t.Fatal(requestErr)
	}

	get, err := http.NewRequest(http.MethodGet, "http://"+n.DeskAddr()+"/api/library?path=race.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	get.Header.Set("X-Weazl-Desk", "1")
	response, err := c.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("read final file status=%d err=%v", response.StatusCode, readErr)
	}
	if !bytes.Equal(got, one) && !bytes.Equal(got, two) {
		t.Fatalf("final replacement is mixed or truncated: got=%d bytes", len(got))
	}
}

type statusError struct{ got int }

func (e *statusError) Error() string { return "unexpected HTTP status" }
