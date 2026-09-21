package desk

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestLibraryAPI(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	s := httptest.NewServer(New(v, lib, nil, "", "", ""))
	t.Cleanup(s.Close)
	payload := []byte("gil-setlist")
	req, _ := http.NewRequest(http.MethodPut, s.URL+"/api/library?path=weazldocs/gil-setlist.md", bytes.NewReader(payload))
	req.Header.Set("X-Weazl-Desk", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("put %d", res.StatusCode)
	}
	res, err = http.Get(s.URL + "/api/library")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Files []fileView `json:"files"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(list.Files) != 1 || list.Files[0].Path != "weazldocs/gil-setlist.md" {
		t.Fatalf("list %+v", list.Files)
	}
	res, err = http.Get(s.URL + "/api/library?path=weazldocs/gil-setlist.md")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	res.Body.Close()
	if buf.String() != string(payload) {
		t.Fatalf("get %q", buf.String())
	}
	req, _ = http.NewRequest(http.MethodGet, s.URL+"/api/library?path=weazldocs/gil-setlist.md", nil)
	req.Header.Set("Range", "bytes=1-4")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ranged, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(ranged) != string(payload[1:5]) {
		t.Fatalf("range status=%d body=%q", res.StatusCode, ranged)
	}
}
