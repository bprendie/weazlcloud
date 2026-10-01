package share

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
)

func TestGalleryGuestHTTPPrivacyAndRetryLifecycle(t *testing.T) {
	store := capsule.New(t.TempDir())
	rec, err := store.MintGallery(capsule.Record{Name: "Album.zip", Label: "Trip", Gate: "passphrase", Limit: 3, Expires: time.Now().Add(time.Hour)}, "secret", []capsule.GallerySource{{Item: capsule.GalleryItem{Name: "photo.jpg", MediaType: "image/jpeg", Size: 8, Revision: 4}, Original: func(w io.Writer) error { _, err := io.WriteString(w, "original"); return err }, Preview: func() ([]byte, string, error) { return []byte("pixels"), "image/jpeg", nil }}})
	if err != nil {
		t.Fatal(err)
	}
	h := New(store)
	request := func(method, route string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/g/"+rec.ID+route, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	page := request(http.MethodGet, "", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Download album") || !strings.Contains(page.Header().Get("Content-Security-Policy"), "nonce-") {
		t.Fatalf("page=%d", page.Code)
	}
	if _, err := exec.LookPath("node"); err == nil {
		html := page.Body.String()
		start := strings.Index(html, "<script nonce=")
		script := html[start:]
		script = script[strings.Index(script, ">")+1 : strings.Index(script, "</script>")]
		cmd := exec.Command("node", "--check")
		cmd.Stdin = strings.NewReader(script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("guest script syntax: %v %s", err, output)
		}
	}
	if response := request(http.MethodPost, "/gallery", map[string]string{"passphrase": "wrong"}); response.Code != 401 {
		t.Fatalf("wrong passphrase=%d", response.Code)
	}
	response := request(http.MethodPost, "/gallery", map[string]string{"passphrase": "secret"})
	var gallery struct {
		Gallery capsule.GalleryManifest `json:"gallery"`
		Session string                  `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &gallery); err != nil || response.Code != 200 || len(gallery.Gallery.Items) != 1 {
		t.Fatalf("gallery=%s error=%v", response.Body, err)
	}
	id := gallery.Gallery.Items[0].ID
	preview := request(http.MethodPost, "/preview/"+id, map[string]string{"session": gallery.Session})
	if preview.Code != 200 || preview.Body.String() != "pixels" || preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview=%d %s", preview.Code, preview.Body)
	}
	for _, route := range []string{"/file", "/original/" + id, "/zip"} {
		if response := request(http.MethodGet, route, nil); response.Code != 404 {
			t.Fatalf("GET transfer %s=%d", route, response.Code)
		}
	}
	prepared := request(http.MethodPost, "/zip/jobs", map[string]any{"session": gallery.Session, "ids": []string{id}})
	var job capsule.GalleryZIPView
	if err := json.Unmarshal(prepared.Body.Bytes(), &job); err != nil || prepared.Code != http.StatusAccepted {
		t.Fatalf("prepare selected ZIP=%d %s error=%v", prepared.Code, prepared.Body, err)
	}
	jobRoute := "/zip/jobs/" + job.ID
	deadline := time.Now().Add(5 * time.Second)
	for job.Status != "ready" && time.Now().Before(deadline) {
		status := request(http.MethodPost, jobRoute, map[string]string{"session": gallery.Session})
		if err := json.Unmarshal(status.Body.Bytes(), &job); err != nil || status.Code != http.StatusOK {
			t.Fatalf("selected ZIP status=%d %s error=%v", status.Code, status.Body, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != "ready" {
		t.Fatalf("selected ZIP not ready: %+v", job)
	}
	for _, route := range []string{"/zip/jobs", jobRoute, jobRoute + "/download"} {
		if response := request(http.MethodGet, route, nil); response.Code != http.StatusNotFound {
			t.Fatalf("GET selected ZIP %s=%d", route, response.Code)
		}
	}
	meta, _ := store.Meta(rec.ID)
	if meta.Used != 0 {
		t.Fatalf("browsing consumed retries: %d", meta.Used)
	}
	selected := request(http.MethodPost, jobRoute+"/download", map[string]string{"session": gallery.Session})
	if selected.Code != http.StatusOK || selected.Header().Get("X-Weazl-Grabs-Remaining") != "2" {
		t.Fatalf("selected ZIP download=%d %s", selected.Code, selected.Body)
	}
	z, err := zip.NewReader(bytes.NewReader(selected.Body.Bytes()), int64(selected.Body.Len()))
	if err != nil || len(z.File) != 1 {
		t.Fatalf("selected ZIP readback: %v", err)
	}
	file, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(data) != "original" {
		t.Fatalf("selected original=%q error=%v", data, err)
	}
	original := request(http.MethodPost, "/original/"+id, map[string]string{"session": gallery.Session})
	if original.Code != 200 || original.Body.String() != "original" || original.Header().Get("X-Weazl-Grabs-Remaining") != "1" {
		t.Fatalf("original=%d %s", original.Code, original.Body)
	}
	archive := request(http.MethodPost, "/zip", map[string]string{"session": gallery.Session})
	if archive.Code != 200 || !bytes.HasPrefix(archive.Body.Bytes(), []byte("PK")) || archive.Header().Get("X-Weazl-Grabs-Remaining") != "0" {
		t.Fatalf("ZIP=%d %s", archive.Code, archive.Body)
	}
	if response := request(http.MethodPost, "/preview/"+id, map[string]string{"session": gallery.Session}); response.Code != 404 {
		t.Fatalf("burn preview=%d", response.Code)
	}
}
