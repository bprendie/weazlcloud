package desk

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/photoingest"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

func smokePhotoNodeFilesystemRestore(t *testing.T, h *Handler, source string, user users.User, deviceID, token, assetID, uploadID, albumID, checkpoint string, spec photoingest.Spec, original []byte, shared **sharedstore.Store) {
	t.Helper()
	ctx := context.Background()
	r := h.registry.For(user)
	caption := "Restored sovereign photo"
	if _, err := r.Lib.UpdatePhoto(ctx, assetID, library.PhotoUpdate{Caption: &caption}, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Lib.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lib.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	if err := r.Lib.AcknowledgePhotoSync(ctx, deviceID, checkpoint, false); err != nil {
		t.Fatal(err)
	}
	detail, err := r.Lib.PhotoDetail(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := r.Lib.Metadata(ctx, detail.Path)
	if err != nil || (*shared != nil) != (file.Reference != nil && file.Reference.Backend == catalog.SharedBackend) {
		t.Fatalf("backup fixture used the wrong storage backend: %+v %v", file.Reference, err)
	}
	job, err := r.Archives.Start([]string{detail.Path})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		job, _, _ = r.Archives.Get(job.ID)
		if job.Status == "ready" {
			break
		}
		if job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("backup archive not settled: %+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	exports, release, err := r.Lib.PreparePhotoExport(ctx, []string{assetID}, false)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := h.caps.MintGallery(capsule.Record{Owner: user.ID, Label: "Restore", Name: "restore.zip", Gate: "open", Limit: 3, Expires: time.Now().Add(time.Hour)}, "", []capsule.GallerySource{{Item: capsule.GalleryItem{Name: exports[0].Name, Size: exports[0].Size, MediaType: exports[0].MediaType}, Original: exports[0].Original}})
	release()
	if err != nil {
		t.Fatal(err)
	}
	manifest, guestToken, err := h.caps.GallerySession(grant.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	guestAuth := capsule.GalleryAuth{Session: guestToken}
	if _, err := h.caps.GalleryOriginal(grant.ID, guestAuth, manifest.Items[0].ID, func(capsule.Record, capsule.GalleryItem) (io.Writer, error) { return io.Discard, nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.saveNodeBase("https://grab.restore.test"); err != nil {
		t.Fatal(err)
	}
	r.LockVault() // drain private work before the filesystem snapshot
	sharedEnabled := *shared != nil
	if sharedEnabled {
		if err := (*shared).Close(); err != nil {
			t.Fatal(err)
		}
	}
	destination := t.TempDir()
	copyPhotoNodeFixture(t, source, destination)
	// Derived indexes are deliberately absent; canonical records must rebuild.
	if err := os.Remove(filepath.Join(destination, "users", user.ID, ".weazl-photos-index.enc")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	restoredUsers, err := users.New(filepath.Join(destination, "users.json"), filepath.Join(destination, "users"))
	if err != nil {
		t.Fatal(err)
	}
	restored := NewMulti(restoredUsers, capsule.New(filepath.Join(destination, "caps")), quota.New(destination), "", "", destination)
	if sharedEnabled {
		store, err := sharedstore.Open(destination, sharedstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.ClearAbandonedHolds(ctx); err != nil {
			t.Fatal(err)
		}
		restored.registry.ConfigureShared(store, true)
	}
	restoredResource := restored.registry.For(user)
	t.Cleanup(restoredResource.LockVault)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: handlerTransport{handler: restored}}
	request := func(method, route string, body any, bearer bool, status int) []byte {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, "http://photos.test"+route, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Weazl-Desk", "1")
		req.Header.Set("Content-Type", "application/json")
		if bearer {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != status {
			t.Fatalf("restored %s %s=%d want=%d: %s %v", method, route, res.StatusCode, status, raw, err)
		}
		return raw
	}
	request("POST", "/api/login", map[string]string{"username": user.Username, "password": "native-password"}, false, 200)
	request("GET", "/api/v1/photos/assets/"+assetID+"/original", nil, true, 401)
	request("POST", "/api/unlock", map[string]string{"passphrase": "native-vault"}, false, 200)
	body := request("GET", "/api/v1/photos/assets/"+assetID+"/original", nil, true, 200)
	if !bytes.Equal(body, original) {
		t.Fatal("filesystem restore changed original bytes")
	}
	var photo library.PhotoItem
	if err := json.Unmarshal(request("GET", "/api/v1/photos/assets/"+assetID, nil, true, 200), &photo); err != nil || photo.Caption != caption || len(photo.Components) != 2 || photo.CapturedAt.Year() != 2013 {
		t.Fatalf("restored metadata=%+v %v", photo, err)
	}
	for _, part := range photo.Components {
		if part.ID == "motion" {
			body := request("GET", "/api/v1/photos/assets/"+part.AssetID+"/original", nil, true, 200)
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != spec.Components[1].SHA256 {
				t.Fatal("filesystem restore changed motion original bytes")
			}
		}
	}
	var members library.PhotoAlbumMembershipPage
	if err := json.Unmarshal(request("GET", "/api/v1/photos/albums/memberships?id="+albumID, nil, true, 200), &members); err != nil || len(members.AssetIDs) != 3 {
		t.Fatalf("restored memberships=%+v %v", members, err)
	}
	folder, err := restoredResource.Lib.Metadata(ctx, "Photos/Private")
	if err != nil || !folder.Hidden {
		t.Fatal("hidden folder did not survive filesystem restore")
	}
	request("GET", "/api/v1/photos/sync?resume=1", nil, true, 200)
	spec.DeviceAssetID, spec.SourceRevision = "phone-asset", "r1"
	var receipt photoingest.View
	if err := json.Unmarshal(request("POST", "/api/v1/photos/uploads", spec, true, 201), &receipt); err != nil || receipt.AssetID != assetID || receipt.Upload.ID != uploadID || receipt.Status != "stored" {
		t.Fatalf("restored receipt lost idempotency=%+v %v", receipt, err)
	}
	archive, err := restoredResource.Archives.OpenDownload(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := io.ReadAll(archive)
	archive.Close()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("restored ZIP=%v", err)
	}
	files := 0
	for _, entry := range z.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		files++
		f, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err = io.ReadAll(f)
		f.Close()
		if err != nil || !bytes.Equal(body, original) {
			t.Fatal("restored encrypted ZIP changed original")
		}
	}
	if files != 1 {
		t.Fatal("restored ZIP lost or duplicated an original")
	}
	if restored.grabBase() != "https://grab.restore.test" {
		t.Fatal("node hostname was not restored")
	}
	restoredResource.LockVault()
	meta, err := restored.caps.Meta(grant.ID)
	if err != nil || meta.Used != 1 {
		t.Fatal("restore reset capsule admission counters")
	}
	var frozen bytes.Buffer
	if _, err := restored.caps.GalleryOriginal(grant.ID, guestAuth, manifest.Items[0].ID, func(capsule.Record, capsule.GalleryItem) (io.Writer, error) { return &frozen, nil }); err != nil || !bytes.Equal(frozen.Bytes(), original) {
		t.Fatalf("restored locked-owner grant changed bytes: %v", err)
	}
	if sharedEnabled {
		*shared, err = sharedstore.Open(source, sharedstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		h.registry.ConfigureShared(*shared, true)
	}
	if err := r.Vault.Unlock([]byte("native-vault")); err != nil {
		t.Fatal(err)
	}
}

func copyPhotoNodeFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
}
