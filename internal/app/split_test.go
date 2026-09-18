package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bprendie/weazlcloud/internal/config"
)

func TestBindSplit(t *testing.T) {
	n, err := Start(config.Config{
		DataDir:   t.TempDir(),
		DeskAddr:  "127.0.0.1:0",
		ShareAddr: "127.0.0.1:0",
		DriveAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })

	desk := get(t, "http://"+n.DeskAddr()+"/")
	if !strings.Contains(desk, "WeazlCloud / the household node") {
		t.Fatalf("desk missing marker %q", desk)
	}
	for _, url := range []string{
		"http://" + n.ShareAddr() + "/",
		"http://" + n.ShareAddr() + "/index.html",
		"http://" + n.ShareAddr() + "/api/library",
		"http://" + n.DriveAddr() + "/index.html",
	} {
		body := get(t, url)
		if strings.Contains(body, "WeazlCloud / the household node") {
			t.Fatalf("%s leaked desk: %s", url, body)
		}
	}
	postUnlock(t, "http://"+n.ShareAddr()+"/unlock", http.StatusNotFound)
	postUnlock(t, "http://"+n.DriveAddr()+"/unlock", http.StatusNotFound)
	postUnlock(t, "http://"+n.ShareAddr()+"/api/forge", http.StatusNotFound)
	postUnlock(t, "http://"+n.ShareAddr()+"/api/unlock", http.StatusNotFound)

	urls := []string{
		"http://" + n.DeskAddr() + "/ready",
		"http://" + n.ShareAddr() + "/ready",
		"http://" + n.DriveAddr() + "/ready",
	}
	var wg sync.WaitGroup
	errc := make(chan error, 120)
	for i := 0; i < 40; i++ {
		for _, u := range urls {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				res, err := http.Get(url)
				if err != nil {
					errc <- err
					return
				}
				res.Body.Close()
			}(u)
		}
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
}

func TestEnsureTempDir(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "restic-tmp")
	t.Setenv("TMPDIR", tmp)
	if err := ensureTempDir(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", tmp)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func postUnlock(t *testing.T, url string, want int) {
	t.Helper()
	res, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("POST %s status %d want %d", url, res.StatusCode, want)
	}
	if strings.Contains(string(body), "WeazlCloud / the household node") {
		t.Fatalf("POST %s leaked desk", url)
	}
}
