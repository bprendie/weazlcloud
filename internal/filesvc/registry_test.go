package filesvc

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/idle"
	"github.com/bprendie/weazlcloud/internal/restic"
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

func TestIdleTrashCleanupPurgesExpiredAndLeavesNewerTrashRestorable(t *testing.T) {
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
	registry := NewRegistry(store)
	resource := registry.For(alice)
	if err := resource.Vault.Forge([]byte("vault-password"), []byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oldContent := []byte("expired trash")
	newContent := []byte("newer trash")
	if _, err := resource.Lib.Put(ctx, "expired.bin", oldContent); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Lib.Put(ctx, "newer.bin", newContent); err != nil {
		t.Fatal(err)
	}
	if err := resource.Lib.Delete("expired.bin"); err != nil {
		t.Fatal(err)
	}
	if err := resource.Lib.Delete("newer.bin"); err != nil {
		t.Fatal(err)
	}
	trash, err := resource.Lib.Trash(ctx)
	if err != nil || len(trash) != 2 {
		t.Fatalf("trash=%+v err=%v", trash, err)
	}
	now := time.Now().UTC()
	for i := range trash {
		deleted := now.Add(-29 * 24 * time.Hour)
		if trash[i].Path == "expired.bin" {
			deleted = now.Add(-31 * 24 * time.Hour)
		}
		trash[i].DeletedAt = &deleted
	}
	plain, err := json.Marshal(struct {
		Files []catalog.File `json:"files"`
	}{Files: trash})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := resource.Vault.Wrap(plain)
	cryptox.Zero(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := cryptox.AtomicWrite(store.CatalogPath(alice), sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	resource.Vault.Lock()

	clock := now
	coordinator := idle.New(time.Minute, func() time.Time { return clock })
	coordinator.Register(registry.CleanupExpiredTrash)
	registry.SetActivityTracker(coordinator.Track)
	clock = clock.Add(2 * time.Minute)
	if !coordinator.RunOnce(ctx) {
		t.Fatal("eligible idle cleanup did not run")
	}
	if resource.Vault.Unlocked() {
		t.Fatal("idle cleanup left the user's vault unlocked")
	}
	if err := resource.Vault.Unlock([]byte("vault-password")); err != nil {
		t.Fatal(err)
	}
	trash, err = resource.Lib.Trash(ctx)
	if err != nil || len(trash) != 1 || trash[0].Path != "newer.bin" {
		t.Fatalf("trash after idle cleanup=%+v err=%v", trash, err)
	}
	if err := resource.Lib.Restore(ctx, "newer.bin"); err != nil {
		t.Fatalf("restore newer Trash: %v", err)
	}
	got, err := resource.Lib.Get(ctx, "newer.bin")
	if err != nil || string(got) != string(newContent) {
		t.Fatalf("restored bytes=%q err=%v", got, err)
	}
	if _, err := resource.Lib.Get(ctx, "expired.bin"); err == nil {
		t.Fatal("expired Trash content remained readable")
	}
	pass, _, err := resource.Vault.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := restic.New().Snapshots(ctx, restic.Repo{Location: store.LibraryPath(alice), Password: pass})
	cryptox.Zero(pass)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("remaining snapshots=%v err=%v", snapshots, err)
	}
}
