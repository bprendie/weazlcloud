package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobilePartsCancellationSurvivesSweepRestartAndPreservesStoredOriginal(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	for _, kind := range []string{"photo", "backup"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
			if err != nil {
				t.Fatal(err)
			}
			owner, err := store.Create("owner", "test-password", true)
			if err != nil {
				t.Fatal(err)
			}
			device, token, err := store.CreateScopedDevice(owner.ID, "phone", []string{users.PhotosWrite, users.PhotosRead, users.BackupWrite, users.FilesWrite})
			if err != nil {
				t.Fatal(err)
			}
			h := NewMulti(store, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
			res := h.registry.For(owner)
			if err := res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { res.LockVault() })
			data := []byte("opaque original must survive cancellation")
			sum := sha256.Sum256(data)
			hash := hex.EncodeToString(sum[:])
			base := "/api/v1/photos/uploads"
			spec := map[string]any{"transport": "parts-v1", "commit_when_complete": true, "device_asset_id": "camera", "source_revision": "r1", "filename": "camera.raw", "size": len(data), "sha256": hash, "original_mode": "opaque-original-v1"}
			request := func(method, path string, body []byte, want int) map[string]json.RawMessage {
				t.Helper()
				r := httptest.NewRequest(method, path, bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("X-Weazl-Desk", "1")
				r.Header.Set("X-Weazl-SHA256", hash)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
				}
				var out map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			if kind == "backup" {
				base = "/api/v1/backups/uploads"
				if err := res.Lib.Mkdir(context.Background(), "Backups"); err != nil {
					t.Fatal(err)
				}
				root, err := res.Lib.Metadata(context.Background(), "Backups")
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(backup.Source{ID: "documents", Name: "Documents", DestinationID: root.EntryID})
				request("POST", "/api/v1/backups/sources", raw, 201)
				spec = map[string]any{"transport": "parts-v1", "commit_when_complete": true, "source_id": "documents", "source_item_id": "document", "source_revision": "r1", "relative_path": "document.txt", "size": len(data), "sha256": hash}
			}
			create := func() (string, []byte) {
				t.Helper()
				raw, _ := json.Marshal(spec)
				out := request("POST", base, raw, 201)
				var view mobileparts.View
				if err := json.Unmarshal(out["transfer"], &view); err != nil || view.ID == "" {
					t.Fatalf("create transfer: %+v %v", view, err)
				}
				return view.ID, raw
			}
			age := func(id string) {
				t.Helper()
				session, err := h.mobileParts.Session(res, id)
				if err != nil {
					t.Fatal(err)
				}
				session.UpdatedAt = time.Now().UTC().Add(-25 * time.Hour)
				private, _ := json.Marshal(session)
				sealed, err := res.Vault.Wrap(private)
				clear(private)
				if err != nil {
					t.Fatal(err)
				}
				if err := cryptox.AtomicWrite(filepath.Join(mobileparts.Root(res), id, "session.enc"), sealed, 0600); err != nil {
					t.Fatal(err)
				}
			}
			id, raw := create()
			request("PUT", base+"/"+id+"/components/original/parts/0", data, 200)
			age(id)
			request("DELETE", base+"/"+id, nil, 200)
			request("DELETE", base+"/"+id, nil, 200)
			status := request("GET", base+"/"+id, nil, 200)
			var cancelled mobileparts.View
			if err := json.Unmarshal(status["transfer"], &cancelled); err != nil || cancelled.Status != "cancelled" {
				t.Fatalf("cancelled status: %+v %v", cancelled, err)
			}
			request("POST", base, raw, 404)
			age(id)
			if err := h.mobileParts.Sweep(res); err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(mobileparts.Root(res), id)
			if _, err := os.Stat(stage); err != nil {
				t.Fatalf("sweep removed durable cancelled engine tombstone: %v", err)
			}
			request("POST", base, raw, 404)
			// Also prove that the coordinator alone prevents resurrection if
			// encrypted transport state is lost independently of the receipt.
			if err := os.RemoveAll(stage); err != nil {
				t.Fatal(err)
			}
			res.LockVault()
			store, err = users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
			if err != nil {
				t.Fatal(err)
			}
			h = NewMulti(store, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
			res = h.registry.For(owner)
			if err := res.Vault.UnlockNode(); err != nil {
				t.Fatal(err)
			}
			request("POST", base, raw, 404)
			if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancelled logical identity resurrected staging: %v", err)
			}
			spec["source_revision"] = "r2"
			storedID, _ := create()
			if storedID == id {
				t.Fatal("new revision reused cancelled identity")
			}
			request("PUT", base+"/"+storedID+"/components/original/parts/0", data, 200)
			session, err := h.mobileParts.Session(res, storedID)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate publication succeeding before the transport stored receipt
			// is saved. DELETE must recover that receipt without deleting the asset.
			ctx, release, ok := h.registry.Enter(context.Background(), owner.ID)
			if !ok {
				t.Fatal("owner unavailable")
			}
			result, err := h.commitMobilePart(ctx, res, owner, session, func(component string) (io.ReadCloser, error) {
				return h.mobileParts.Open(ctx, res, storedID, component)
			})
			release()
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Path string                `json:"path"`
				File struct{ Path string } `json:"file"`
			}
			if err := json.Unmarshal(result, &receipt); err != nil {
				t.Fatal(err)
			}
			path := receipt.Path
			if kind == "backup" {
				path = receipt.File.Path
			}
			rejected := request("DELETE", base+"/"+storedID, nil, 409)
			if string(rejected["code"]) != `"already_stored"` {
				t.Fatalf("stored cancellation code: %s", rejected["code"])
			}
			stored, err := h.mobileParts.Status(res, storedID, device.ID)
			if err != nil || stored.Status != "stored" || len(stored.Result) == 0 {
				t.Fatalf("stored engine recovery: %+v %v", stored, err)
			}
			got, err := res.Lib.Get(context.Background(), path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("cancellation deleted or changed stored original: %v", err)
			}
		})
	}
}
