package desk

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadyAndDeskHTML(t *testing.T) {
	s := httptest.NewServer(New(nil, nil, nil, "", "", ""))
	t.Cleanup(s.Close)
	res, err := http.Get(s.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("ready %d %q", res.StatusCode, body)
	}
	res, err = http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("desk html %q", body)
	}
	if res.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing csp")
	}
}
