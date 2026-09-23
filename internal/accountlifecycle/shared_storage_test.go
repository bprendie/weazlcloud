package accountlifecycle

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestDeleteSharedOwnerDrainsJobsAndPreservesOtherOwner(t *testing.T) {
	root := t.TempDir()
	userStore, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := userStore.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := userStore.Create("bob", "bob-password", true)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sharedstore.Open(root, sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	q := quota.New(root)
	registry := filesvc.NewRegistry(userStore, q)
	registry.ConfigureShared(store, true)
	capsules := capsule.New(filepath.Join(root, "capsules"))
	uploads := upload.New(filepath.Join(root, "uploads"), registry.For, q, userStore.Count)
	manager := New(userStore, registry, capsules, uploads)
	aliceResource, bobResource := registry.For(alice), registry.For(bob)
	for _, resource := range []*filesvc.Resource{aliceResource, bobResource} {
		if err = resource.Vault.Forge([]byte("shared-vault"), []byte("shared-vault")); err != nil {
			t.Fatal(err)
		}
	}
	payload := bytes.Repeat([]byte("one immutable object shared by two owner catalogs"), 1024)
	if _, err = aliceResource.Lib.Put(context.Background(), "shared.bin", payload); err != nil {
		t.Fatal(err)
	}
	if _, err = bobResource.Lib.Put(context.Background(), "shared.bin", payload); err != nil {
		t.Fatal(err)
	}
	if _, err = uploads.Create(alice, "unfinished.iso", 1024, ""); err != nil {
		t.Fatal(err)
	}
	job, err := aliceResource.Archives.Start([]string{"shared.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Delete(context.Background(), alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := userStore.User(alice.ID); ok {
		t.Fatal("deleted account remains")
	}
	if _, err = os.Stat(filepath.Join(root, "users", alice.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted owner data remains: %v", err)
	}
	if _, _, ok := aliceResource.Archives.Get(job.ID); ok {
		t.Fatal("archive job survived account drain")
	}
	var got bytes.Buffer
	if err = bobResource.Lib.StreamTo(context.Background(), "shared.bin", &got); err != nil || !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("other owner's shared reference was damaged: %v", err)
	}
	count, err := store.ObjectCount(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("shared payload count=%d err=%v", count, err)
	}
	if _, err = uploads.Create(bob, "survives.iso", 32, ""); err != nil {
		t.Fatalf("other owner's uploads unavailable: %v", err)
	}
	if _, err = store.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
}
