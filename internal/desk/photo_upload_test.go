package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestVersionedPhotoUploadIsIdempotentAndAppliesCaptureMetadata(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewMulti(us, capsule.New(filepath.Join(dir, "capsules")), quota.New(dir), "", "", dir)
	serverURL := "http://photos.test"
	t.Cleanup(func() {
		for _, user := range us.Users() {
			handler.registry.For(user).LockVault()
		}
	})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: handlerTransport{handler: handler}}
	postJSON := func(url string, body any) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, serverURL+url, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Weazl-Desk", "1")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := postJSON("/api/bootstrap", map[string]string{"username": "photo", "password": "photo-password", "vault_passphrase": "photo-vault", "confirm": "photo-vault"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap=%d", res.StatusCode)
	}
	res.Body.Close()
	res = postJSON("/api/unlock", map[string]string{"passphrase": "photo-vault"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlock=%d", res.StatusCode)
	}
	res.Body.Close()

	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 20, 12))); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(imageBytes.Bytes())
	createBody := map[string]any{
		"device_id": "iphone-17", "device_asset_id": "asset-0001", "filename": "photo.png",
		"size": imageBytes.Len(), "sha256": hex.EncodeToString(hash[:]), "root_id": "root:photos",
	}
	res = postJSON("/api/v1/photos/uploads", createBody)
	var created struct {
		Upload struct {
			ID string `json:"id"`
		} `json:"upload"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || created.Upload.ID == "" {
		t.Fatalf("photo create=%d response=%+v", res.StatusCode, created)
	}
	res = postJSON("/api/v1/photos/uploads", createBody)
	var repeated struct {
		Upload struct {
			ID string `json:"id"`
		} `json:"upload"`
	}
	_ = json.NewDecoder(res.Body).Decode(&repeated)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || repeated.Upload.ID != created.Upload.ID {
		t.Fatalf("idempotent create=%d response=%+v", res.StatusCode, repeated)
	}

	chunk := imageBytes.Bytes()
	chunkHash := sha256.Sum256(chunk)
	patch, _ := http.NewRequest(http.MethodPatch, serverURL+"/api/v1/photos/uploads/"+created.Upload.ID+"/components/original", bytes.NewReader(chunk))
	patch.Header.Set("X-Weazl-Desk", "1")
	patch.Header.Set("Upload-Offset", "0")
	patch.Header.Set("Upload-Chunk-SHA256", hex.EncodeToString(chunkHash[:]))
	res, err = client.Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("photo chunk=%d", res.StatusCode)
	}
	res = postJSON("/api/v1/photos/uploads/"+created.Upload.ID+"/finalize", map[string]any{"captured_at": "2013-04-05T06:07:08Z", "offset_known": false})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("photo finalize=%d", res.StatusCode)
	}
	res.Body.Close()
	res, err = client.Get(serverURL + "/api/photos?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []struct {
			ID            string `json:"id"`
			Path          string `json:"path"`
			CaptureSource string `json:"capture_source"`
			CapturedAt    string `json:"captured_at"`
			Offset        *int   `json:"capture_offset_minutes"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].CaptureSource != "client" || page.Items[0].CapturedAt == "" || page.Items[0].Offset != nil {
		t.Fatalf("photo page=%d %+v", res.StatusCode, page)
	}
	res = postJSON("/api/v1/photos/assets/"+page.Items[0].ID, map[string]string{"caption": "lake day"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("photo edit=%d", res.StatusCode)
	}
	res, err = client.Get(serverURL + "/api/v1/photos/search?q=lake*")
	if err != nil {
		t.Fatal(err)
	}
	var search struct {
		Items []struct {
			ID      string `json:"id"`
			Caption string `json:"caption"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&search); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(search.Items) != 1 || search.Items[0].ID != page.Items[0].ID || search.Items[0].Caption != "lake day" {
		t.Fatalf("photo search=%d %+v", res.StatusCode, search)
	}
	originalRequest, _ := http.NewRequest(http.MethodGet, serverURL+"/api/v1/photos/assets/"+page.Items[0].ID+"/original", nil)
	originalRequest.Header.Set("Range", "bytes=1-7")
	res, err = client.Do(originalRequest)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(rangeBody, imageBytes.Bytes()[1:8]) {
		t.Fatalf("photo original range=%d %q", res.StatusCode, rangeBody)
	}
	headRequest, _ := http.NewRequest(http.MethodHead, serverURL+"/api/v1/photos/assets/"+page.Items[0].ID+"/original", nil)
	res, err = client.Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(headBody) != 0 {
		t.Fatalf("photo original HEAD=%d %q", res.StatusCode, headBody)
	}

	if err := handler.saveNodeBase("https://grab.test"); err != nil {
		t.Fatal(err)
	}
	smokePhotoSelectionArchive(t, client, serverURL, page.Items[0].ID, imageBytes.Bytes())
	ownerResource := handler.registry.For(us.Users()[0])
	smokeCorruptPhotoGallery(t, handler, postJSON)
	if _, err := ownerResource.Lib.Put(context.Background(), "Documents/private.txt", []byte("private document")); err != nil {
		t.Fatal(err)
	}
	privateJob, err := ownerResource.Archives.Start([]string{"Documents/private.txt"})
	if err != nil {
		t.Fatal(err)
	}
	_, deviceToken, err := us.CreateDevice(us.Users()[0].ID, "archive-scope-test")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", serverURL+"/api/v1/photos/archives?id="+privateJob.ID, nil)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("Photos device credential accessed a Library archive")
	}
	res = postJSON("/api/v1/photos/grabs", map[string]any{"ids": []string{page.Items[0].ID}, "title": "Lake", "gate": "open", "grabs": 2})
	var grab struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &grab); err != nil || res.StatusCode != http.StatusCreated || grab.ID == "" {
		t.Fatalf("photo grab=%d %s error=%v", res.StatusCode, raw, err)
	}
	manifest, token, err := handler.caps.GallerySession(grab.ID, "")
	if err != nil || len(manifest.Items) != 1 {
		t.Fatalf("gallery manifest=%+v error=%v", manifest, err)
	}
	var preview bytes.Buffer
	if _, err := handler.caps.GalleryPreview(grab.ID, capsule.GalleryAuth{Session: token}, manifest.Items[0].ID, &preview); err != nil {
		t.Fatal(err)
	}
	if _, _, err := image.Decode(bytes.NewReader(preview.Bytes())); err != nil {
		t.Fatalf("gallery derivative could not decode: %v", err)
	}
	user := us.Users()[0]
	handler.registry.For(user).LockVault()
	var frozen bytes.Buffer
	if _, err := handler.caps.GalleryOriginal(grab.ID, capsule.GalleryAuth{Session: token}, manifest.Items[0].ID, func(capsule.Record, capsule.GalleryItem) (io.Writer, error) { return &frozen, nil }); err != nil || !bytes.Equal(frozen.Bytes(), imageBytes.Bytes()) {
		t.Fatalf("locked vault changed frozen original: %v", err)
	}
	smokePhotoHiddenOwnerIsolation(t, handler, client, serverURL, postJSON, page.Items[0].ID, grab.ID, token, manifest.Items[0].ID)

}
