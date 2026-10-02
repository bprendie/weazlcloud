package desk

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func mobileDeskFixture(t *testing.T) *Handler {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("test"), []byte("test")); err != nil {
		t.Fatal(err)
	}
	l := library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)
	return New(v, l, capsule.New(filepath.Join(dir, "capsules")), "https://grab.test", "", "")
}
func mobileDeskRequest(t *testing.T, h *Handler, method, route, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, route, bytes.NewBufferString(body))
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	if !h.tryMobileGrabs(w, r) {
		t.Fatal("route not handled")
	}
	return w
}
func TestMobileGrabFilesFoldersAndRestart(t *testing.T) {
	h := mobileDeskFixture(t)
	ctx := context.Background()
	for _, p := range []string{"docs/a.txt", "docs/sub/b.txt"} {
		if _, err := h.lib.Put(ctx, p, []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"file", "folder"} {
		t.Run(kind, func(t *testing.T) {
			source := "docs"
			if kind == "file" {
				source = "docs/a.txt"
			}
			body := `{"path":"` + source + `","kind":"` + kind + `","gate":"open","grabs":3}`
			w := mobileDeskRequest(t, h, "POST", "/api/capsules", kind, body)
			if w.Code != 200 {
				t.Fatalf("mint=%d %s", w.Code, w.Body.String())
			}
			var view struct{ ID string }
			if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			// Retry after replacing the original and recreating the capsule store.
			if _, err := h.lib.Put(ctx, "docs/a.txt", []byte("replacement")); err != nil {
				t.Fatal(err)
			}
			originalStore := h.caps
			h.caps = capsule.New(filepath.Join(filepath.Dir(h.vault.Path()), "capsules"))
			retry := mobileDeskRequest(t, h, "POST", "/api/capsules", kind, body)
			if !bytes.Equal(retry.Body.Bytes(), w.Body.Bytes()) {
				t.Fatal("retry changed result")
			}
			status := mobileDeskRequest(t, h, "GET", "/api/v1/grabs/operations", kind, "")
			if status.Code != 200 || !bytes.Equal(status.Body.Bytes(), w.Body.Bytes()) {
				t.Fatal("status changed result")
			}
			var frozen bytes.Buffer
			_, err := h.caps.StreamGrab(view.ID, "", func(capsule.Record) (io.Writer, error) { return &frozen, nil })
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" && frozen.String() != "docs/a.txt" {
				t.Fatalf("not frozen: %q", frozen.String())
			}
			if kind == "folder" {
				z, err := zip.NewReader(bytes.NewReader(frozen.Bytes()), int64(frozen.Len()))
				if err != nil {
					t.Fatal(err)
				}
				found := map[string]string{}
				for _, f := range z.File {
					if f.FileInfo().IsDir() {
						continue
					}
					r, _ := f.Open()
					b, _ := io.ReadAll(r)
					r.Close()
					found[f.Name] = string(b)
				}
				if found["a.txt"] != "replacement" || found["sub/b.txt"] != "docs/sub/b.txt" {
					t.Fatalf("folder=%v", found)
				}
			}
			h.caps = originalStore
		})
	}
	r := httptest.NewRequest("POST", "/api/capsules", bytes.NewBufferString(`{}`))
	if h.tryMobileGrabs(httptest.NewRecorder(), r) {
		t.Fatal("unkeyed browser intercepted")
	}
}

func TestMobileGrabCanonicalAndValidation(t *testing.T) {
	h := mobileDeskFixture(t)
	if _, err := h.lib.Put(context.Background(), "a.txt", nil); err != nil {
		t.Fatal(err)
	}
	a := mobileDeskRequest(t, h, "POST", "/api/capsules", "canonical", `{"path":"a.txt","grabs":2,"gate":"open"}`)
	b := mobileDeskRequest(t, h, "POST", "/api/capsules", "canonical", "{\n\"gate\":\"open\",\"grabs\":2,\"path\":\"a.txt\"}")
	if a.Code != 200 || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Fatalf("canonical=%d/%d %s", a.Code, b.Code, a.Body.String())
	}
	conflict := mobileDeskRequest(t, h, "POST", "/api/capsules", "canonical", `{"path":"a.txt","grabs":3,"gate":"open"}`)
	if conflict.Code != 409 {
		t.Fatalf("conflict=%d", conflict.Code)
	}
	for _, body := range []string{`{} {}`, `{"album":"first-200"}`, `null`} {
		w := mobileDeskRequest(t, h, "POST", "/api/capsules", "invalid", body)
		if w.Code != 400 {
			t.Fatalf("invalid=%s status=%d", body, w.Code)
		}
	}
	h.publicBase = ""
	w := mobileDeskRequest(t, h, "POST", "/api/capsules", "hostname", `{"path":"a.txt"}`)
	if w.Code == 200 {
		t.Fatal("hostname missing accepted")
	}
}

func TestMobileZIPFileWriterBoundedChunking(t *testing.T) {
	var encoded bytes.Buffer
	z := zip.NewWriter(&encoded)
	entry, err := z.CreateHeader(&zip.FileHeader{Name: "deep/file", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 1<<20)
	entry.Write(payload)
	z.Close()
	var got bytes.Buffer
	out := &mobileZIPFileWriter{dst: &got, left: int64(len(payload))}
	for _, b := range encoded.Bytes() {
		if _, err := out.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if out.left != 0 || !bytes.Equal(got.Bytes(), payload) || len(out.header) != 30 {
		t.Fatal("stream mismatch")
	}
}

func TestMobileGrabCapturedFileRevisionSurvivesReplacement(t *testing.T) {
	h := mobileDeskFixture(t)
	ctx := context.Background()
	if _, err := h.lib.Put(ctx, "a.txt", []byte("original")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/capsules", bytes.NewBufferString(`{"path":"a.txt","grabs":2}`))
	if _, err := canonicalMobileGrab(httptest.NewRecorder(), r); err != nil {
		t.Fatal(err)
	}
	rec, oldSource, err := h.sealFor(r, mintBody{Path: "a.txt", Grabs: 2}, h.vault, h.lib)
	if err != nil {
		t.Fatal(err)
	}
	rec, source, release, err := mobileGrabPrepare(r, h.lib)(rec, oldSource)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := h.lib.Put(ctx, "a.txt", []byte("replaced")); err != nil {
		t.Fatal(err)
	}
	frozen, err := h.caps.MintStream(rec, "", source)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := h.caps.StreamGrab(frozen.ID, "", func(capsule.Record) (io.Writer, error) { return &out, nil }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "original" {
		t.Fatalf("followed replacement: %q", out.String())
	}
}
