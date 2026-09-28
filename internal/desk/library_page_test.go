package desk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestFolderPageHTTPReturnsBoundedDisplayMetadata(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("page-http-pass"), []byte("page-http-pass")); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.enc")
	c := catalog.New(catalogPath, v)
	for _, name := range []string{"b.txt", "a.txt"} {
		if err := c.Put(catalog.File{Path: name, Size: 10, Hash: "secret-hash", Present: true}); err != nil {
			t.Fatal(err)
		}
	}
	l := library.New(filepath.Join(root, "repo"), catalogPath, v)
	h := New(v, l, capsule.New(filepath.Join(root, "capsules")), "", "", "")
	request := httptest.NewRequest("GET", "/api/library/page?limit=1&sort=name", nil)
	response := httptest.NewRecorder()
	h.listFolderPage(response, request)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Files []map[string]any `json:"files"`
		Next  string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Files) != 1 || body.Files[0]["path"] != "a.txt" || body.Next == "" {
		t.Fatalf("unexpected page: %+v", body)
	}
	if _, leaked := body.Files[0]["hash"]; leaked {
		t.Fatal("page exposed a content hash")
	}
	searchRequest := httptest.NewRequest("GET", "/api/library/search/page?q=a.txt", nil)
	searchResponse := httptest.NewRecorder()
	h.searchFolderPageFor(searchResponse, searchRequest, v, l)
	if searchResponse.Code != http.StatusOK || !json.Valid(searchResponse.Body.Bytes()) {
		t.Fatalf("search response status=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
	var searchBody struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &searchBody); err != nil {
		t.Fatal(err)
	}
	if len(searchBody.Files) != 1 || searchBody.Files[0]["path"] != "a.txt" {
		t.Fatalf("search returned unexpected rows: %+v", searchBody.Files)
	}
}
