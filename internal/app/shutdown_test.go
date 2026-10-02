package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/config"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestShutdownCancelsEventsExportsJobsAndRetainsReadyArchive(t *testing.T) {
	root := t.TempDir()
	n, err := Start(config.Config{DataDir: root, DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0", StorageBackend: "shared-experimental"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	post := func(path string, body any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, "http://"+n.DeskAddr()+path, bytes.NewReader(data))
		req.Header.Set("X-Weazl-Desk", "1")
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			t.Fatal("fixture HTTP status", response.StatusCode)
		}
	}
	post("/api/bootstrap", map[string]string{"username": "alice", "password": "shutdown-password", "vault_passphrase": "shutdown-vault", "confirm": "shutdown-vault"})
	post("/api/unlock", map[string]string{"passphrase": "shutdown-vault"})
	store, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Users()[0]
	resource := n.registry.For(owner)
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if _, err := resource.Lib.Put(context.Background(), "Photos/image.png", png); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Lib.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, err := resource.Lib.PhotoPreparation()
		if err != nil || time.Now().After(deadline) {
			t.Fatal("preparation did not finish", state, err)
		}
		if state.Status == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	archive, err := resource.Archives.Start([]string{"Photos/image.png"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		state, _, _ := resource.Archives.Get(archive.ID)
		if state.Status == "ready" {
			break
		}
		if state.Status == "failed" || time.Now().After(deadline) {
			t.Fatal("archive did not finish", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	ownerRoot := filepath.Dir(store.LibraryPath(owner))
	journal := filepath.Join(ownerRoot, ".weazl-photo-jobs.journal.enc")
	wrapped, err := os.ReadFile(filepath.Join(ownerRoot, ".weazl-photo-jobs.enc"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := resource.Vault.Unwrap(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	var beforeSnapshot struct{ Sequence uint64 }
	if err := json.Unmarshal(plain, &beforeSnapshot); err != nil {
		t.Fatal(err)
	}
	clear(plain)
	retained := map[string][]byte{}
	for _, ext := range []string{".enc", ".wza"} {
		path := filepath.Join(resource.Lib.ArchiveDir(), archive.ID+ext)
		retained[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	stream, err := client.Get("http://" + n.DeskAddr() + "/api/library/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatal("event stream not admitted")
	}
	done := make(chan error, 1)
	go func() { done <- n.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("event stream prevented graceful shutdown")
	}
	if _, err := io.ReadAll(stream.Body); err != nil {
		t.Fatal("event stream was not closed cleanly", err)
	}
	if resource.Vault.Unlocked() {
		t.Fatal("shutdown retained an unlocked vault")
	}
	if _, _, ok := n.registry.Enter(context.Background(), "new-owner"); ok {
		t.Fatal("shutdown accepted new owner work")
	}
	if info, err := os.Stat(journal); err != nil || info.Size() != 0 {
		t.Fatal("clean shutdown did not export journal", err)
	}
	if err := resource.Vault.Unlock([]byte("shutdown-vault")); err != nil {
		t.Fatal(err)
	}
	defer resource.Vault.Lock()
	wrapped, err = os.ReadFile(filepath.Join(ownerRoot, ".weazl-photo-jobs.enc"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err = resource.Vault.Unwrap(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var snapshot struct {
		Version  int
		Queue    photos.JobQueue
		Sequence uint64
	}
	if json.Unmarshal(plain, &snapshot) != nil || snapshot.Version != 1 || snapshot.Sequence <= beforeSnapshot.Sequence || len(snapshot.Queue.Jobs) != 1 || snapshot.Queue.Jobs[0].Status != photos.JobSucceeded {
		t.Fatal("rollback snapshot does not include completed journal job")
	}
	for path, before := range retained {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("shutdown discarded/changed a retained archive", err)
		}
	}
}
