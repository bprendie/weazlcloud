package filesvc

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestEncryptedArchiveRecoversQueuedAndReadyJobs(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("archive-test"), []byte("archive-test")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(root, "repo"), filepath.Join(root, "catalog.enc"), v)
	t.Cleanup(func() { lib.PrepareVaultLock(); v.Lock(); lib.ForgetVaultSession() })
	if _, err := lib.Put(context.Background(), "private-name.txt", []byte("private file bytes")); err != nil {
		t.Fatal(err)
	}
	m := NewArchiveManager(lib)
	// Keep the worker queued; after its process-local state disappears the
	// sealed manifest must be sufficient to re-acquire immutable references.
	m.slots <- struct{}{}
	m.slots <- struct{}{}
	view, err := m.Start([]string{"private-name.txt"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	original := m.jobs[view.ID]
	original.cancel()
	m.mu.Unlock()
	<-original.done
	recovered := NewArchiveManager(lib)
	var ready ArchiveJobView
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		current, _, ok := recovered.Get(view.ID)
		if !ok {
			t.Fatal("queued encrypted checkpoint did not load")
		}
		if current.Status == "ready" {
			ready = current
			break
		}
		if current.Status == "failed" {
			t.Fatalf("recovery failed: %s", current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ready.ID == "" || ready.ExpiresAt.Sub(time.Now()) > 91*time.Minute {
		t.Fatal("archive did not become ready with 90-minute retention")
	}
	reader, err := recovered.OpenDownload(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil || len(z.File) != 1 {
		t.Fatalf("ZIP=%+v %v", z, err)
	}
	entry, _ := z.File[0].Open()
	body, _ := io.ReadAll(entry)
	entry.Close()
	if z.File[0].Name != "private-name.txt" || string(body) != "private file bytes" {
		t.Fatal("recovered ZIP changed originals")
	}
	for _, extension := range []string{".enc", ".wza"} {
		raw, err := os.ReadFile(filepath.Join(lib.ArchiveDir(), view.ID+extension))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("private-name")) || bytes.Contains(raw, body) {
			t.Fatal("archive metadata/payload contains plaintext")
		}
	}
	restarted := NewArchiveManager(lib)
	loaded, _, ok := restarted.Get(view.ID)
	if !ok || loaded.Status != "ready" {
		t.Fatal("ready archive did not survive coordinator restart")
	}
	reader, err = restarted.OpenDownload(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(3, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rangeBytes := make([]byte, 17)
	if _, err := io.ReadFull(reader, rangeBytes); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if !bytes.Equal(rangeBytes, plain[3:20]) {
		t.Fatal("resumed ZIP range differs")
	}
	// An explicit cancellation is a durable terminal decision, unlike losing
	// a process while its queue checkpoint still says preparing.
	cancelledManager := NewArchiveManager(lib)
	cancelledManager.slots <- struct{}{}
	cancelledManager.slots <- struct{}{}
	cancelledJob, err := cancelledManager.Start([]string{"private-name.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !cancelledManager.Cancel(cancelledJob.ID) {
		t.Fatal("queued archive was not cancelled")
	}
	cancelledManager.mu.Lock()
	cancelledDone := cancelledManager.jobs[cancelledJob.ID].done
	cancelledManager.mu.Unlock()
	<-cancelledDone
	reloadedCancelled := NewArchiveManager(lib)
	view, _, ok = reloadedCancelled.Get(cancelledJob.ID)
	if !ok || view.Status != "cancelled" {
		t.Fatal("restart resurrected a cancelled archive")
	}
	v.Lock()
	if _, err := restarted.OpenDownload(context.Background(), view.ID); err == nil {
		t.Fatal("locked vault opened cached ZIP")
	}
	if _, err := restarted.CleanupExpired(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib.ArchiveDir(), view.ID+".wza")); !os.IsNotExist(err) {
		t.Fatal("expired encrypted archive retained")
	}
}
