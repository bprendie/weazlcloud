package desk

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestResumableUploadSurvivesNodeRestart(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	newServer := func() *httptest.Server {
		h := NewMulti(us, capsule.New(filepath.Join(dir, "capsules")), quota.New(dir), "", "", dir)
		return httptest.NewServer(h)
	}
	s := newServer()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path string, body any) *http.Response {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, s.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Weazl-Desk", "1")
		res, requestErr := c.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return res
	}
	res := post("/api/bootstrap", map[string]string{"username": "resume", "password": "resume-password", "vault_passphrase": "resume-vault", "confirm": "resume-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "resume-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock status=%d", res.StatusCode)
	}
	res.Body.Close()

	payload := []byte("resume this upload after the node restarts")
	res = post("/api/uploads", map[string]any{"path": "images/disk.iso", "size": len(payload)})
	var session struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || session.ID == "" {
		t.Fatalf("create upload status=%d session=%+v", res.StatusCode, session)
	}
	first := len(payload) / 2
	patch := func(offset int, body []byte) *http.Response {
		req, _ := http.NewRequest(http.MethodPatch, s.URL+"/api/uploads/"+session.ID, bytes.NewReader(body))
		req.Header.Set("X-Weazl-Desk", "1")
		req.Header.Set("Upload-Offset", stringOffset(offset))
		req.Header.Set("Content-Type", "application/octet-stream")
		response, requestErr := c.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	res = patch(0, payload[:first])
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first chunk status=%d", res.StatusCode)
	}
	res = patch(0, payload[first:])
	var conflict map[string]any
	_ = json.NewDecoder(res.Body).Decode(&conflict)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict || conflict["offset"] != float64(first) {
		t.Fatalf("wrong offset status=%d body=%v", res.StatusCode, conflict)
	}

	s.Close()
	s = newServer()
	defer s.Close()
	res = post("/api/unlock", map[string]string{"passphrase": "resume-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("re-unlock status=%d", res.StatusCode)
	}
	res.Body.Close()
	res, err = c.Get(s.URL + "/api/uploads/" + session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Offset int64 `json:"offset"`
	}
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || status.Offset != int64(first) {
		t.Fatalf("restart offset status=%d body=%+v", res.StatusCode, status)
	}
	res = patch(first, payload[first:])
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second chunk status=%d", res.StatusCode)
	}
	res = post("/api/uploads/"+session.ID+"/finalize", map[string]any{})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("finalize status=%d", res.StatusCode)
	}
	res = post("/api/uploads/"+session.ID+"/finalize", map[string]any{})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("repeated finalize status=%d", res.StatusCode)
	}
	res, err = c.Get(s.URL + "/api/library?path=images/disk.iso")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || !bytes.Equal(got, payload) {
		t.Fatalf("stored payload status=%d err=%v body=%q", res.StatusCode, err, got)
	}
}

func stringOffset(n int) string {
	return strconv.Itoa(n)
}
