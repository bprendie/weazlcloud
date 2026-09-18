package share

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShareIsNotTheDesk(t *testing.T) {
	s := httptest.NewServer(New(nil))
	t.Cleanup(s.Close)
	for _, path := range []string{"/", "/index.html", "/unlock", "/g/abc"} {
		req, _ := http.NewRequest(http.MethodGet, s.URL+path, nil)
		if path == "/unlock" {
			req, _ = http.NewRequest(http.MethodPost, s.URL+path, nil)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(body), "WeazlCloud / the household node") {
			t.Fatalf("%s leaked desk: %s", path, body)
		}
		if path != "/ready" && res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status %d", path, res.StatusCode)
		}
	}
	res, err := http.Get(s.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Fatalf("ready body %q", body)
	}
}
