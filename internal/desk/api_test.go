package desk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestForgeUnlockLockKit(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	s := httptest.NewServer(New(v, nil, nil, "", "", ""))
	t.Cleanup(s.Close)
	pass := `{"passphrase":"nug-nug","confirm":"nug-nug"}`
	if code, body := post(t, s.URL+"/api/forge", pass, false); code != http.StatusForbidden {
		t.Fatalf("csrf %d %s", code, body)
	}
	code, body := post(t, s.URL+"/api/forge", pass, true)
	if code != 200 || !strings.Contains(body, "forged") {
		t.Fatalf("forge %d %s", code, body)
	}
	code, body = get(t, s.URL+"/api/status")
	if code != 200 || strings.Contains(body, "nug-nug") || strings.Contains(body, dir) {
		t.Fatalf("status canary %d %s", code, body)
	}
	post(t, s.URL+"/api/lock", `{}`, true)
	code, body = post(t, s.URL+"/api/unlock", `{"passphrase":"nug-nug"}`, true)
	if code != 200 {
		t.Fatalf("unlock %d %s", code, body)
	}
	code, body = post(t, s.URL+"/api/kit", `{"passphrase":"nug-nug"}`, true)
	if code != 200 {
		t.Fatalf("kit %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "weazlcloud-recovery.wzck")); err != nil {
		t.Fatal(err)
	}
}

func post(t *testing.T, url, body string, mark bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if mark {
		req.Header.Set("X-Weazl-Desk", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.String()
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(res.Body).Decode(&v)
	b, _ := json.Marshal(v)
	return res.StatusCode, string(b)
}
