package sharedstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func testRoot(t *testing.T) string {
	t.Helper()
	base := os.Getenv("WEAZLCLOUD_SHAREDSTORE_TEST_ROOT")
	if base == "" {
		t.Skip("run scripts/sharedstore-smoke.sh to use a disposable Docker volume")
	}
	root, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func testVault(t *testing.T, root, name string) *vault.Vault {
	t.Helper()
	dir := filepath.Join(root, "users", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("pass-"+name), []byte("pass-"+name)); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSharedDedupeOwnershipAndVaultLifecycle(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	alice, bob := testVault(t, root, "alice"), testVault(t, root, "bob")
	auth, err := users.New(filepath.Join(root, "accounts.json"), filepath.Join(root, "account-data"))
	if err != nil {
		t.Fatal(err)
	}
	aliceUser, err := auth.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	bobUser, err := auth.Create("bob", "bob-password", false)
	if err != nil {
		t.Fatal(err)
	}
	adminUser, err := auth.Create("admin", "admin-password", true)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("whole-object-fixture-"), 140000)
	sum := sha256.Sum256(data)
	upload := func(owner, entry string, v *vault.Vault) Prepared {
		t.Helper()
		p, e := store.Prepare(ctx, owner, v, entry, 1, bytes.NewReader(data), int64(len(data)))
		if e != nil {
			t.Fatal(e)
		}
		if e = store.MarkPublished(ctx, p.Operation); e != nil {
			t.Fatal(e)
		}
		if e = store.Commit(ctx, p.Operation); e != nil {
			t.Fatal(e)
		}
		return p
	}
	a := upload(aliceUser.ID, "entry-a", alice)
	b := upload(bobUser.ID, "entry-b", bob)
	if a.Reference.ObjectID == b.Reference.ObjectID || a.Reference.Version != chunkFormatVersion || b.Reference.Version != chunkFormatVersion {
		t.Fatal("identical files did not receive independent versioned manifests")
	}
	if n, e := store.ObjectCount(ctx); e != nil || n < 1 {
		t.Fatalf("objects=%d err=%v", n, e)
	}
	var physical int64
	objects, err := os.ReadDir(filepath.Join(root, "shared-objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		info, e := object.Info()
		if e != nil {
			t.Fatal(e)
		}
		physical += info.Size()
	}
	if len(objects) < 2 || physical >= int64(len(data))*2 {
		t.Fatalf("no content-defined chunk savings: objects=%d physical=%d logical=%d", len(objects), physical, len(data)*2)
	}
	for _, path := range []string{filepath.Join(root, "shared-index", "node.keys"), filepath.Join(root, "shared-index", "index.db"), filepath.Join(root, "shared-objects", objects[0].Name())} {
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("shared file %s has permissions %o", filepath.Base(path), info.Mode().Perm())
		}
	}
	t.Logf("chunk dedupe: two logical copies=%d bytes, authenticated chunks and private manifests=%d bytes", len(data)*2, physical)
	for _, tc := range []struct {
		name    string
		v       *vault.Vault
		owner   string
		ref     Reference
		wantErr bool
	}{{"alice", alice, aliceUser.ID, a.Reference, false}, {"bob", bob, bobUser.ID, b.Reference, false}, {"copied-reference", bob, bobUser.ID, a.Reference, true}, {"admin", testVault(t, root, "admin"), adminUser.ID, a.Reference, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			e := store.Read(ctx, tc.owner, tc.v, tc.ref, &out)
			if tc.wantErr {
				if e == nil {
					t.Fatal("unexpectedly authorized")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			got := sha256.Sum256(out.Bytes())
			if hex.EncodeToString(got[:]) != hex.EncodeToString(sum[:]) {
				t.Fatal("read hash mismatch")
			}
		})
	}
	alice.Lock()
	if err = store.Read(ctx, aliceUser.ID, alice, a.Reference, io.Discard); !errors.Is(err, ErrDenied) {
		t.Fatalf("locked read: %v", err)
	}
	if err = alice.Unlock([]byte("pass-alice")); err != nil {
		t.Fatal(err)
	}
	if err = alice.Rekey([]byte("pass-alice"), []byte("new-alice"), []byte("new-alice")); err != nil {
		t.Fatal(err)
	}
	alice.Lock()
	if err = alice.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	if err = auth.ChangePassword(aliceUser.ID, "alice-password", "alice-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.Authenticate("alice", "alice-new-password"); err != nil {
		t.Fatal(err)
	}
	if err = store.Read(ctx, aliceUser.ID, alice, a.Reference, io.Discard); err != nil {
		t.Fatalf("account password change affected vault data: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "users", "alice", "shared-index")); !os.IsNotExist(err) {
		t.Fatal("personal vault contains node-wide shared keys")
	}
	if err = store.Release(ctx, aliceUser.ID, "entry-a", 1); err != nil {
		t.Fatal(err)
	}
	if err = store.Read(ctx, aliceUser.ID, alice, a.Reference, io.Discard); err == nil {
		t.Fatal("released owner still reads")
	}
	if err = store.Read(ctx, bobUser.ID, bob, b.Reference, io.Discard); err != nil {
		t.Fatalf("other owner damaged: %v", err)
	}
}

func TestConcurrentIdenticalUploadsConverge(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	s1, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	users := []*vault.Vault{testVault(t, root, "one"), testVault(t, root, "two")}
	stores := []*Store{s1, s2}
	data := bytes.Repeat([]byte("concurrent"), 160000)
	start := make(chan struct{})
	var wg sync.WaitGroup
	refs := make([]Prepared, 2)
	errs := make([]error, 2)
	for i := range refs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, e := stores[i].Prepare(ctx, []string{"one", "two"}[i], users[i], "entry", 1, bytes.NewReader(data), int64(len(data)))
			if e == nil {
				e = stores[i].MarkPublished(ctx, p.Operation)
			}
			if e == nil {
				e = stores[i].Commit(ctx, p.Operation)
			}
			refs[i], errs[i] = p, e
		}(i)
	}
	close(start)
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if refs[0].Reference.ObjectID == refs[1].Reference.ObjectID {
		t.Fatal("simultaneous duplicate files reused a private manifest")
	}
	if n, e := s1.ObjectCount(ctx); e != nil || n < 1 {
		t.Fatalf("objects=%d err=%v", n, e)
	}
}
