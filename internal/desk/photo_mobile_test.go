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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/photoingest"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestNativePhotoSimulatorResumesPairAndPreservesRevisions(t *testing.T) {
	for _, backend := range []string{"restic", "shared"} {
		t.Run(backend, func(t *testing.T) { runNativePhotoSimulator(t, backend) })
	}
}

func runNativePhotoSimulator(t *testing.T, backend string) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	var shared *sharedstore.Store
	if backend == "shared" {
		shared, err = sharedstore.Open(dir, sharedstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		h.registry.ConfigureShared(shared, true)
		t.Cleanup(func() { shared.Close() })
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: handlerTransport{handler: h}}
	request := func(method, route string, body []byte, token string, headers map[string]string, expected int) []byte {
		t.Helper()
		r, _ := http.NewRequest(method, "http://photos.test"+route, bytes.NewReader(body))
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("%s %s status=%d want=%d: %s", method, route, response.StatusCode, expected, raw)
		}
		return raw
	}
	jsonBody := func(v any) []byte { b, _ := json.Marshal(v); return b }
	request("POST", "/api/bootstrap", jsonBody(map[string]string{"username": "native", "password": "native-password", "vault_passphrase": "native-vault", "confirm": "native-vault"}), "", nil, 201)
	request("POST", "/api/unlock", jsonBody(map[string]string{"passphrase": "native-vault"}), "", nil, 200)
	user := us.Users()[0]
	resource := h.registry.For(user)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := resource.Lib.Drain(ctx); err != nil {
			t.Error(err)
		}
		resource.LockVault()
	})
	device, token, err := us.CreateDevice(user.ID, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := us.CreateDevice(user.ID, "iPhone 2")
	if err != nil {
		t.Fatal(err)
	}
	album, err := resource.Lib.SavePhotoAlbum(context.Background(), catalog.Album{Title: "Phone album"})
	if err != nil {
		t.Fatal(err)
	}
	var still bytes.Buffer
	if err := png.Encode(&still, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	// Minimal ISO-BMFF source header. Preview failure is isolated from backup.
	motion := append([]byte{0, 0, 0, 24}, []byte("ftypqt  \x00\x00\x00\x00qt  ")...)
	part := func(id, name, kind string, body []byte) photoingest.Component {
		sum := sha256.Sum256(body)
		return photoingest.Component{ID: id, Filename: name, MediaType: kind, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	}
	spec := photoingest.Spec{DeviceAssetID: "phone-asset", SourceRevision: "r1", RootID: "root:photos", CapturedAt: "2013-02-03T12:00:00-05:00", OffsetKnown: true, AlbumIDs: []string{album.ID}, Components: []photoingest.Component{part("original", "same.png", "image/png", still.Bytes()), part("motion", "same.mov", "video/quicktime", motion)}}
	var created photoingest.View
	if err := json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(spec), token, nil, 201), &created); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/photos/uploads/" + created.Upload.ID
	var repeated photoingest.View
	json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(spec), token, nil, 201), &repeated)
	if repeated.Upload.ID != created.Upload.ID {
		t.Fatal("matching creation did not recover logical session")
	}
	request("GET", base, nil, otherToken, nil, 404)
	conflict := spec
	conflict.Components = append([]photoingest.Component(nil), spec.Components...)
	conflict.Components[0].Size++
	request("POST", "/api/v1/photos/uploads", jsonBody(conflict), token, nil, 409)
	chunk := func(component, offset string, body []byte, expected int, hashOverride string) {
		t.Helper()
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		if hashOverride != "" {
			hash = hashOverride
		}
		request("PATCH", base+"/components/"+component, body, token, map[string]string{"Upload-Offset": offset, "Upload-Chunk-SHA256": hash}, expected)
	}
	chunk("original", "1", still.Bytes()[:20], 409, "")
	chunk("original", "0", still.Bytes()[:20], 422, strings.Repeat("0", 64))
	chunk("original", "0", still.Bytes()[:20], 200, "")
	h.uploads = upload.New(filepath.Join(dir, "uploads"), func(u users.User) *filesvc.Resource { return h.registry.For(u) }, nil, us.Count)
	h.photoUploads = photoingest.New(h.uploads) // byte engine and coordinator restart
	var resumed photoingest.View
	json.Unmarshal(request("GET", base, nil, token, nil, 200), &resumed)
	if resumed.Components[0].Offset != 20 {
		t.Fatal("durable offset was lost")
	}
	chunk("original", "20", still.Bytes()[20:], 200, "")
	request("POST", base+"/finalize", []byte("{}"), token, nil, 409)
	page, err := resource.Lib.PhotoPage(context.Background(), 10, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("partial pair surfaced: %+v %v", page, err)
	}
	chunk("motion", "0", motion, 200, "")
	var final photoingest.View
	json.Unmarshal(request("POST", base+"/finalize", []byte("{}"), token, nil, 200), &final)
	if final.AssetID == "" || final.Status != "stored" {
		t.Fatalf("pair not durably committed: %+v", final)
	}
	h.photoUploads = photoingest.New(h.uploads)
	var lostResponse photoingest.View
	json.Unmarshal(request("POST", base+"/finalize", []byte("{}"), token, nil, 200), &lostResponse)
	json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(spec), token, nil, 201), &repeated)
	if lostResponse.AssetID != final.AssetID || lostResponse.Revision != final.Revision || repeated.AssetID != final.AssetID {
		t.Fatal("lost final response duplicated or changed the asset")
	}
	page, err = resource.Lib.PhotoPage(context.Background(), 10, "", "")
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Components) != 2 || page.Items[0].CapturedAt.Year() != 2013 || page.Items[0].DeviceID != device.ID {
		t.Fatalf("logical pair=%+v %v", page, err)
	}
	members, err := resource.Lib.PhotoAlbumMemberships(context.Background(), album.ID, "", 10, false)
	if err != nil || len(members.AssetIDs) != 1 || members.AssetIDs[0] != final.AssetID {
		t.Fatalf("album missing pair: %+v %v", members, err)
	}
	snapshot, err := os.ReadFile(us.CatalogPath(user))
	if err != nil {
		t.Fatal(err)
	}
	// A new source revision stores a new original without overwriting r1.
	spec.SourceRevision = "r2"
	json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(spec), token, nil, 201), &repeated)
	if repeated.Upload.ID == created.Upload.ID {
		t.Fatal("source revision did not create independent session")
	}
	base = "/api/v1/photos/uploads/" + repeated.Upload.ID
	chunk("original", "0", still.Bytes(), 200, "")
	chunk("motion", "0", motion, 200, "")
	var secondRevision photoingest.View
	json.Unmarshal(request("POST", base+"/finalize", []byte("{}"), token, nil, 200), &secondRevision)
	if secondRevision.AssetID == final.AssetID || secondRevision.Status != "stored" {
		t.Fatal("source revision overwrote original")
	}
	// Another authorized phone can back up the same filename independently.
	spec.DeviceAssetID = "second-phone-asset"
	json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(spec), otherToken, nil, 201), &repeated)
	for _, component := range []struct {
		id   string
		body []byte
	}{{"original", still.Bytes()}, {"motion", motion}} {
		sum := sha256.Sum256(component.body)
		request("PATCH", "/api/v1/photos/uploads/"+repeated.Upload.ID+"/components/"+component.id, component.body, otherToken, map[string]string{"Upload-Offset": "0", "Upload-Chunk-SHA256": hex.EncodeToString(sum[:])}, 200)
	}
	var otherPhoto photoingest.View
	json.Unmarshal(request("POST", "/api/v1/photos/uploads/"+repeated.Upload.ID+"/finalize", []byte("{}"), otherToken, nil, 200), &otherPhoto)
	if otherPhoto.AssetID == final.AssetID || otherPhoto.AssetID == secondRevision.AssetID {
		t.Fatal("devices collided")
	}
	request("GET", "/api/v1/photos/assets/"+final.AssetID+"/original", nil, token, nil, 200)
	members, err = resource.Lib.PhotoAlbumMemberships(context.Background(), album.ID, "", 10, false)
	if err != nil || len(members.AssetIDs) != 3 {
		t.Fatalf("album commits lost: %+v %v", members, err)
	}

	// Exhausted capacity fails creation without claiming a stored original.
	h.uploads = upload.New(filepath.Join(dir, "uploads"), func(u users.User) *filesvc.Resource { return h.registry.For(u) }, quota.New(dir), us.Count)
	h.photoUploads = photoingest.New(h.uploads)
	huge := photoingest.Spec{DeviceAssetID: "too-large-for-node", Components: []photoingest.Component{part("original", "huge.png", "image/png", still.Bytes())}}
	huge.Components[0].Size = 1 << 60
	request("POST", "/api/v1/photos/uploads", jsonBody(huge), token, nil, 507)
	// Declared image extensions cannot smuggle non-image bytes into Photos.
	bad := photoingest.Spec{DeviceAssetID: "bad-media", Components: []photoingest.Component{part("original", "fake.png", "image/png", []byte("not image bytes"))}}
	json.Unmarshal(request("POST", "/api/v1/photos/uploads", jsonBody(bad), token, nil, 201), &repeated)
	badHash := sha256.Sum256([]byte("not image bytes"))
	request("PATCH", "/api/v1/photos/uploads/"+repeated.Upload.ID+"/components/original", []byte("not image bytes"), token, map[string]string{"Upload-Offset": "0", "Upload-Chunk-SHA256": hex.EncodeToString(badHash[:])}, 200)
	request("POST", "/api/v1/photos/uploads/"+repeated.Upload.ID+"/finalize", []byte("{}"), token, nil, 400)

	// Receipts must not leak phone IDs, album names, capture dates or filenames.
	receipts, _ := os.ReadDir(resource.Lib.PhotoIngestDir())
	for _, entry := range receipts {
		raw, _ := os.ReadFile(filepath.Join(resource.Lib.PhotoIngestDir(), entry.Name()))
		for _, value := range []string{"phone-asset", "same.png", "2013-02-03"} {
			if bytes.Contains(raw, []byte(value)) {
				t.Fatalf("plaintext ingest receipt: %s", value)
			}
		}
	}
	// Reconcile bounded snapshots, save a device checkpoint, then detect a
	// restored server catalog instead of resurrecting a future client cache.
	cursor, checkpoint := "", ""
	for i := 0; i < 100; i++ {
		var syncPage library.PhotoSyncPage
		json.Unmarshal(request("GET", "/api/v1/photos/sync?limit=2&cursor="+cursor, nil, token, nil, 200), &syncPage)
		if syncPage.Checkpoint != "" {
			checkpoint = syncPage.Checkpoint
			break
		}
		cursor = syncPage.NextCursor
	}
	if checkpoint == "" {
		t.Fatal("initial sync did not complete")
	}
	var delta library.PhotoSyncPage
	json.Unmarshal(request("GET", "/api/v1/photos/sync?limit=200&cursor="+checkpoint, nil, token, nil, 200), &delta)
	checkpoint = delta.Checkpoint
	request("POST", "/api/v1/photos/sync/checkpoint", jsonBody(map[string]any{"checkpoint": checkpoint}), token, nil, 200)
	request("GET", "/api/v1/photos/sync?resume=1", nil, token, nil, 200)
	smokePhotoNodeFilesystemRestore(t, h, dir, user, device.ID, token, final.AssetID, created.Upload.ID, album.ID, checkpoint, spec, still.Bytes(), &shared)
	resource.LockVault()
	if err := cryptox.AtomicWrite(us.CatalogPath(user), snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	if err := resource.Vault.Unlock([]byte("native-vault")); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/v1/photos/sync?cursor="+checkpoint, nil, token, nil, 409)
	request("GET", "/api/v1/photos/sync", nil, token, nil, 200)
	request("GET", "/api/v1/photos/assets/"+final.AssetID+"/original", nil, token, nil, 200)

	resource.LockVault()
	request("GET", base, nil, token, nil, 401)
	if err := us.RevokeDevice(user.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	request("GET", base, nil, token, nil, 401)
}
