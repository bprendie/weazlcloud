package drive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriveIsNotUnlock(t *testing.T) {
	s := httptest.NewServer(New())
	t.Cleanup(s.Close)
	post, _ := http.NewRequest(http.MethodPost, s.URL+"/unlock", nil)
	res, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("unlock %d %q", res.StatusCode, body)
	}
	res, err = http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("root %d", res.StatusCode)
	}
	if strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("root leaked desk %q", body)
	}
	res, err = http.Get(s.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Fatalf("ready %q", body)
	}
}
