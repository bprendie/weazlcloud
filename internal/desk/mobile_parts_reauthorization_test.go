package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobilePartsReauthorizationRejectsChangedCommitAndResumesSameIntent(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	for _, renewal := range []string{"owner-reauthorization", "expired-admission-after-rotation", "expired-stage-owner-reauthorization"} {
		t.Run(renewal, func(t *testing.T) {
			dir := t.TempDir()
			store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
			if err != nil {
				t.Fatal(err)
			}
			owner, err := store.Create("owner", "test-password", true)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			store.SetClock(func() time.Time { return now })
			device, token, err := store.CreateScopedDevice(owner.ID, "phone", []string{users.BackupWrite, users.FilesWrite})
			if err != nil {
				t.Fatal(err)
			}
			h := NewMulti(store, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
			res := h.registry.For(owner)
			if err := res.Vault.Forge([]byte("test-vault"), []byte("test-vault")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { res.LockVault() })
			if err := res.Lib.Mkdir(context.Background(), "Backups"); err != nil {
				t.Fatal(err)
			}
			root, err := res.Lib.Metadata(context.Background(), "Backups")
			if err != nil {
				t.Fatal(err)
			}
			request := func(method, path string, body []byte, want int) map[string]json.RawMessage {
				t.Helper()
				r := httptest.NewRequest(method, path, bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("X-Weazl-Desk", "1")
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
			source, _ := json.Marshal(backup.Source{ID: "documents", Name: "Documents", DestinationID: root.EntryID})
			request("POST", "/api/v1/backups/sources", source, 201)
			data := []byte("immutable original awaiting publication")
			sum := sha256.Sum256(data)
			hash := hex.EncodeToString(sum[:])
			in := struct {
				backup.Spec
				CommitWhenComplete bool `json:"commit_when_complete"`
			}{backup.Spec{SourceID: "documents", ItemID: "document", SourceRevision: "r1", RelativePath: "document.txt", Size: int64(len(data)), SHA256: hash, Transport: "parts-v1"}, true}
			raw, _ := json.Marshal(in)
			created := request("POST", "/api/v1/backups/uploads", raw, 201)
			var transfer mobileparts.View
			if err := json.Unmarshal(created["transfer"], &transfer); err != nil {
				t.Fatal(err)
			}
			// Stage a complete part without starting a worker. The old grant must
			// remain unusable until a successful admission explicitly refreshes it.
			r := httptest.NewRequest(http.MethodPut, "/api/v1/backups/uploads/"+transfer.ID+"/components/original/parts/0", bytes.NewReader(data))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Weazl-Desk", "1")
			r.Header.Set("X-Weazl-SHA256", hash)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("stage part: %d %s", w.Code, w.Body.String())
			}
			before, err := h.mobileParts.Session(res, transfer.ID)
			if err != nil || before.Status != "queued" || before.Spec.Components[0].ReceivedBytes != int64(len(data)) {
				t.Fatalf("staged session: %+v %v", before, err)
			}
			var prior mobileIntent
			if err := json.Unmarshal(before.Spec.Payload, &prior); err != nil {
				t.Fatal(err)
			}
			expiredStage := renewal == "expired-stage-owner-reauthorization"
			stageDir := filepath.Join(mobileparts.Root(res), transfer.ID)
			if expiredStage {
				// Age only this owner's encrypted fixture; no wall-clock sleep or
				// production clock hook is needed to exercise staging expiry.
				before.UpdatedAt = time.Now().UTC().Add(-25 * time.Hour)
				private, err := json.Marshal(before)
				if err != nil {
					t.Fatal(err)
				}
				sealed, err := res.Vault.Wrap(private)
				clear(private)
				if err != nil {
					t.Fatal(err)
				}
				if err := cryptox.AtomicWrite(filepath.Join(stageDir, "session.enc"), sealed, 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() map[string]string {
				t.Helper()
				entries, err := os.ReadDir(stageDir)
				if err != nil {
					t.Fatal(err)
				}
				out := make(map[string]string)
				for _, entry := range entries {
					if entry.IsDir() {
						t.Fatal("unexpected staging subdirectory", entry.Name())
					}
					sealed, err := os.ReadFile(filepath.Join(stageDir, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					out[entry.Name()] = string(sealed)
				}
				return out
			}
			sealedBefore := snapshot()
			replacement := strings.Repeat("a", 64)
			if renewal == "owner-reauthorization" || expiredStage {
				now = now.Add(time.Hour)
				cookie, err := store.Login(owner)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest("POST", "/api/v1/devices/"+device.ID+"/reauthorize", nil)
				r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
				if _, err := store.ReauthorizeDevice(r, device.ID, replacement, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				now = device.ExpiresAt.Add(-time.Minute)
				r := httptest.NewRequest("POST", "/api/v1/devices/"+device.ID+"/rotate", nil)
				r.Header.Set("Authorization", "Bearer "+token)
				rotated, err := store.RotateDevice(r, device.ID, "renew", device.Generation, replacement)
				if err != nil || rotated.AuthorizationVersion != device.AuthorizationVersion {
					t.Fatalf("same-epoch rotation: %+v %v", rotated, err)
				}
				now = device.ExpiresAt
			}
			token = replacement
			if err := store.CheckDeviceGrant(prior.Grant, users.BackupWrite, users.FilesWrite); err == nil {
				t.Fatal("old admitted grant unexpectedly valid")
			}
			in.CommitWhenComplete = false
			changed, _ := json.Marshal(in)
			conflict := request("POST", "/api/v1/backups/uploads", changed, 409)
			if string(conflict["code"]) != `"idempotency_conflict"` {
				t.Fatalf("conflict code: %s", conflict["code"])
			}
			afterConflict, err := h.mobileParts.Session(res, transfer.ID)
			if (expiredStage && !errors.Is(err, mobileparts.ErrExpired)) || (!expiredStage && err != nil) || !reflect.DeepEqual(before, afterConflict) {
				t.Fatalf("rejected replay mutated grant, timestamp, or session: %v", err)
			}
			if !reflect.DeepEqual(sealedBefore, snapshot()) {
				t.Fatal("rejected replay rewrote encrypted staging files")
			}
			resumed := request("POST", "/api/v1/backups/uploads", raw, 201)
			var resumedTransfer mobileparts.View
			if err := json.Unmarshal(resumed["transfer"], &resumedTransfer); err != nil || resumedTransfer.ID != transfer.ID {
				t.Fatalf("resumed identity: %+v %v", resumedTransfer, err)
			}
			after, err := h.mobileParts.Session(res, transfer.ID)
			if err != nil {
				t.Fatal(err)
			}
			var fresh mobileIntent
			if err := json.Unmarshal(after.Spec.Payload, &fresh); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(prior.Grant, fresh.Grant) || store.CheckDeviceGrant(fresh.Grant, users.BackupWrite, users.FilesWrite) != nil {
				t.Fatal("same-intent replay did not persist fresh usable authority")
			}
			if !bytes.Equal(prior.File, fresh.File) || !bytes.Equal(prior.Photo, fresh.Photo) || after.UpdatedAt.Before(before.UpdatedAt) {
				t.Fatal("resume changed immutable intent or regressed activity")
			}
			if expiredStage {
				if after.ID != before.ID || after.OwnerID != before.OwnerID || after.Spec.DeviceID != before.Spec.DeviceID || after.Key == before.Key || after.Status != "uploading" || after.Spec.Components[0].ReceivedParts != 0 || after.Spec.Components[0].ReceivedBytes != 0 {
					t.Fatal("expired stage did not restart with same identity, fresh key, and zero progress")
				}
				files := snapshot()
				if len(files) != 1 || files["session.enc"] == "" {
					t.Fatal("expired ciphertext or part receipts survived restart")
				}
				status := request("GET", "/api/v1/backups/uploads/"+transfer.ID, nil, 200)
				var current mobileparts.View
				if err := json.Unmarshal(status["transfer"], &current); err != nil || current.Status != "uploading" || current.Components[0].ReceivedBytes != 0 {
					t.Fatalf("restarted HTTP status: %+v %v", current, err)
				}
				return
			}
			// Only the private authorization and admission timestamp may change.
			after.Spec.Payload = before.Spec.Payload
			after.UpdatedAt = before.UpdatedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("resume changed key, identity, state, accepted bytes, or creation time")
			}
		})
	}
}
