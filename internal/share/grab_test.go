package share

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
)

func TestGrabOpenBurns(t *testing.T) {
	store := capsule.New(t.TempDir())
	rec, err := store.Mint(capsule.Record{
		Name: "nug.md", Kind: "file", Gate: "open",
		Expires: time.Now().Add(time.Hour), Limit: 1, Size: 4,
	}, "", []byte("nug!"))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(New(store))
	t.Cleanup(s.Close)
	res, err := http.Get(s.URL + "/g/" + rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || bytes.Contains(body, []byte("WeazlCloud / the household node")) {
		t.Fatalf("page %d", res.StatusCode)
	}
	if !bytes.Contains(body, []byte("NO ACCOUNT")) {
		t.Fatalf("grab page %s", body)
	}
	res, err = http.Post(s.URL+"/g/"+rec.ID+"/file", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(got) != "nug!" || res.Header.Get("X-Weazl-Grabs-Remaining") != "0" {
		t.Fatalf("file %d %q", res.StatusCode, got)
	}
	res, err = http.Post(s.URL+"/g/"+rec.ID+"/file", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("burn %d", res.StatusCode)
	}
}

func TestGrabMetaJSON(t *testing.T) {
	store := capsule.New(t.TempDir())
	rec, err := store.Mint(capsule.Record{
		Name: "pic.jpg", Kind: "file", Gate: "open",
		Expires: time.Now().Add(time.Hour), Limit: 2, Size: 1,
	}, "", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(New(store))
	t.Cleanup(s.Close)
	res, err := http.Get(s.URL + "/g/" + rec.ID + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var meta map[string]any
	if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["name"] != "pic.jpg" || meta["status"] != "live" {
		t.Fatalf("%v", meta)
	}
}

func TestGrabTokenMustBeGeneratedHex(t *testing.T) {
	valid := strings.Repeat("a", 32)
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/g/" + valid + "/meta", true},
		{"/g/" + strings.Repeat("a", 31) + "/meta", false},
		{"/g/" + strings.Repeat("g", 32) + "/meta", false},
		{"/g/" + strings.Repeat("a", 32) + "x/meta", false},
		{"/g/${alert(1)}/meta", false},
	} {
		id, rest := grabParts(tc.path)
		if (id != "" && rest == "meta") != tc.want {
			t.Fatalf("grabParts(%q) = %q, %q; want valid=%t", tc.path, id, rest, tc.want)
		}
	}
}

func TestGrabPageDoesNotBuildMarkupFromMembers(t *testing.T) {
	store := capsule.New(t.TempDir())
	rec, err := store.Mint(capsule.Record{
		Name: "folder", Kind: "folder", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 1,
		Files: []capsule.Member{{Title: `<img src=x onerror=alert(1)>`, Size: 1, Kind: "FILE"}},
	}, "", []byte("zip"))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(New(store))
	t.Cleanup(s.Close)
	res, err := http.Get(s.URL + "/g/" + rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(body)
	if strings.Contains(page, "innerHTML = j.files") || !strings.Contains(page, "title.textContent=f.title") {
		t.Fatalf("grab page still renders member names as markup")
	}
}
