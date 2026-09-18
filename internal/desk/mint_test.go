package desk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/share"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestMintAndGrab(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	store := capsule.New(filepath.Join(dir, "capsules"))
	desk := httptest.NewServer(New(v, lib, store, "https://grab.example", "", ""))
	t.Cleanup(desk.Close)
	grab := httptest.NewServer(share.New(store))
	t.Cleanup(grab.Close)
	put, _ := http.NewRequest(http.MethodPut, desk.URL+"/api/library?path=weazldocs/nug.md", bytes.NewBufferString("hello gil"))
	put.Header.Set("X-Weazl-Desk", "1")
	res, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("put %d", res.StatusCode)
	}
	mint, _ := http.NewRequest(http.MethodPost, desk.URL+"/api/capsules", bytes.NewBufferString(`{"path":"weazldocs/nug.md","kind":"file","gate":"open","label":"recipient","expiry":"24h","grabs":1}`))
	mint.Header.Set("X-Weazl-Desk", "1")
	mint.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(mint)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("mint %d %v", res.StatusCode, out)
	}
	id, _ := out["id"].(string)
	url, _ := out["url"].(string)
	if id == "" || url != "https://grab.example/g/"+id {
		t.Fatalf("url %v", out)
	}
	res, err = http.Post(grab.URL+"/g/"+id+"/file", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := ioRead(res)
	if res.StatusCode != 200 || body != "hello gil" {
		t.Fatalf("grab %d %q", res.StatusCode, body)
	}
}

func ioRead(res *http.Response) (string, error) {
	defer res.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(res.Body)
	return buf.String(), err
}
