package migration

import (
	"bytes"
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/vault"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPendingUploadsReadsEncryptedStatusesWithoutRewriting(t *testing.T) {
	root := t.TempDir()
	store := newMigrationUsers(t, root)
	user, err := store.Create("inventory", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(store.VaultPath(user), store.NodeKeyPath(user))
	if err = v.Forge([]byte("inventory-vault"), []byte("inventory-vault")); err != nil {
		t.Fatal(err)
	}
	defer v.Lock()
	dir := filepath.Join(root, "uploads", user.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	statuses := []string{"uploading", "ready", "finalizing", "complete"}
	for i, status := range statuses {
		id := strings.Repeat(string(rune('a'+i)), 32)
		plain, _ := json.Marshal(map[string]any{"format": 2, "id": id, "owner_id": user.ID, "status": status})
		wrapped, err := v.Wrap(plain)
		clear(plain)
		if err != nil {
			t.Fatal(err)
		}
		raw := append([]byte("WZU2\n"), wrapped...)
		name := filepath.Join(dir, id+".json")
		if err = os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
		original[name] = raw
	}
	nativeRoot := filepath.Join(filepath.Dir(store.LibraryPath(user)), ".weazl-mobile-parts")
	for i, status := range []string{"queued", "stored", "cancelled", "uploading", "verifying", "failed"} {
		id := strings.Repeat(string(rune('0'+i)), 32)
		plain, _ := json.Marshal(map[string]any{"version": 1, "id": id, "owner_id": user.ID, "status": status})
		wrapped, err := v.Wrap(plain)
		clear(plain)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(nativeRoot, id, "session.enc")
		if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(name, wrapped, 0600); err != nil {
			t.Fatal(err)
		}
		original[name] = wrapped
	}
	n, err := pendingUploads(root, store, user)
	if err != nil || n != 7 {
		t.Fatalf("pending: %d %v", n, err)
	}
	for name, raw := range original {
		after, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(after, raw) {
			t.Fatal("inventory rewrote staging")
		}
	}
	futureNative := filepath.Join(nativeRoot, strings.Repeat("9", 32), "session.enc")
	if err = os.MkdirAll(filepath.Dir(futureNative), 0700); err != nil {
		t.Fatal(err)
	}
	futurePlain, _ := json.Marshal(map[string]any{"version": 2, "id": strings.Repeat("9", 32), "owner_id": user.ID, "status": "stored"})
	futureRaw, err := v.Wrap(futurePlain)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(futureNative, futureRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = pendingUploads(root, store, user); err == nil {
		t.Fatal("future native session accepted")
	}
	afterNative, _ := os.ReadFile(futureNative)
	if !bytes.Equal(afterNative, futureRaw) {
		t.Fatal("future native session rewritten")
	}
	if err = os.RemoveAll(filepath.Dir(futureNative)); err != nil {
		t.Fatal(err)
	}
	future := filepath.Join(dir, strings.Repeat("e", 32)+".json")
	plain, _ := json.Marshal(map[string]any{"format": 3, "status": "complete"})
	wrapped, err := v.Wrap(plain)
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte("WZU2\n"), wrapped...)
	if err = os.WriteFile(future, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = pendingUploads(root, store, user); err == nil {
		t.Fatal("future manifest accepted")
	}
	after, _ := os.ReadFile(future)
	if !bytes.Equal(after, raw) {
		t.Fatal("future manifest rewritten")
	}
}
