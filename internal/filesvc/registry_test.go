package filesvc

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestRegistrySharesEachUserResourceAcrossProtocols(t *testing.T) {
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := store.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Create("bob", "bob-password", false)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(store)
	if r.For(alice) != r.For(alice) {
		t.Fatal("same user did not receive one shared resource")
	}
	if r.For(alice) == r.For(bob) {
		t.Fatal("different users received the same resource")
	}
}

func TestMaintenanceUsesStoredNodeKeyAndRelocksVault(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := store.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(store.VaultPath(alice), store.NodeKeyPath(alice))
	if err := v.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	r := NewRegistry(store)
	if err := r.CleanupExpiredTrash(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v.Unlocked() {
		t.Fatal("maintenance left the user's vault unlocked")
	}
	if err := v.Unlock([]byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	if err := r.CleanupExpiredTrash(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !v.Unlocked() {
		t.Fatal("maintenance changed a vault that was already unlocked")
	}
}
