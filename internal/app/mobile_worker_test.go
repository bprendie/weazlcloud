package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/config"
	"github.com/bprendie/weazlcloud/internal/idle"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Real app startup must launch autonomous finalization, without a test calling
// Handler.RunUploads or runMobileParts, and without a client finalize/status poll.
func TestStartAutonomouslyFinalizesMobileParts(t *testing.T) {
	root := t.TempDir()
	us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("mobile", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := us.CreateScopedDevice(u.ID, "phone", []string{users.PhotosRead, users.PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(us.VaultPath(u), us.NodeKeyPath(u))
	if err := v.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	n, err := Start(config.Config{DataDir: root, DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0", StorageBackend: "shared-experimental"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Error(err)
		}
	})
	res := n.registry.For(u)
	if err := res.Vault.Unlock([]byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	if n.uploadWorker == nil {
		t.Fatal("server did not bind the upload worker")
	}
	body := []byte("private byte-preserving camera original")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	client := &http.Client{Timeout: 20 * time.Second}
	request := func(method, path string, data []byte, want int) map[string]json.RawMessage {
		t.Helper()
		r, err := http.NewRequest(method, "http://"+n.DeskAddr()+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("X-Weazl-SHA256", hash)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d", method, path, response.StatusCode, want)
		}
		result := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	spec, _ := json.Marshal(map[string]any{"transport": "parts-v1", "commit_when_complete": true, "device_asset_id": "server-start", "filename": "camera.raw", "size": len(body), "sha256": hash, "hidden": true, "original_mode": "opaque-original-v1"})
	created := request("POST", "/api/v1/photos/uploads", spec, 201)
	var view mobileparts.View
	if err := json.Unmarshal(created["transfer"], &view); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/api/v1/photos/uploads/"+view.ID+"/components/original/parts/0", body, 200)
	manager := mobileparts.New(nil)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("server startup never finalized the accepted original")
		case <-poll.C:
			status, err := manager.Status(res, view.ID, device.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.Status == "stored" {
				page, err := res.Lib.PhotoPage(context.Background(), 20, "", "")
				if err != nil || len(page.Items) != 0 {
					t.Fatal("hidden original appeared in timeline", err)
				}
				return
			}
		}
	}
}

func TestShutdownWaitsForTrackedUploadWorker(t *testing.T) {
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	n := &Node{activity: idle.New(time.Minute, nil), uploadWorker: func(ctx context.Context) { close(entered); <-ctx.Done(); close(canceled); <-finish }}
	n.startIdleMaintenance(context.Background())
	<-entered
	done := make(chan error, 1)
	go func() { done <- n.Close() }()
	<-canceled
	select {
	case <-done:
		t.Fatal("shutdown returned before upload worker finished")
	default:
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-n.idleDone:
	default:
		t.Fatal("shutdown did not wait for both tracked workers")
	}
}
