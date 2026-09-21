package filesvc

import (
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/users"
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
