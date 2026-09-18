package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestForgeUnlockLock(t *testing.T) {
	dir := t.TempDir()
	v := New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	pass := []byte("correct horse")
	if err := v.Forge(pass, pass); err != nil {
		t.Fatal(err)
	}
	restic, drive, err := v.Secrets()
	if err != nil || len(restic) != 32 || len(drive) != 32 {
		t.Fatalf("secrets %v %d %d", err, len(restic), len(drive))
	}
	raw, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, pass) || bytes.Contains(raw, restic) || bytes.Contains(raw, drive) {
		t.Fatal("canary in vault file")
	}
	v.Lock()
	if v.Unlocked() {
		t.Fatal("still unlocked")
	}
	if _, _, err := v.Secrets(); err != ErrLocked {
		t.Fatalf("locked secrets %v", err)
	}
	if err := v.Unlock([]byte("wrong")); err != ErrPass {
		t.Fatalf("wrong pass %v", err)
	}
	if err := v.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	r2, d2, err := v.Secrets()
	if err != nil || !bytes.Equal(r2, restic) || !bytes.Equal(d2, drive) {
		t.Fatal("secrets changed")
	}
}

func TestNodeKeyUnlock(t *testing.T) {
	dir := t.TempDir()
	v := New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	pass := []byte("nug")
	if err := v.Forge(pass, []byte("nug")); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	if err := v.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	if !v.Unlocked() {
		t.Fatal("node key did not unlock")
	}
}

func TestEmptyAndMismatch(t *testing.T) {
	v := New(filepath.Join(t.TempDir(), "vault.json"), filepath.Join(t.TempDir(), "node.key"))
	if err := v.Forge(nil, nil); err != ErrEmpty {
		t.Fatalf("empty %v", err)
	}
	if err := v.Forge([]byte("a"), []byte("b")); err != ErrMismatch {
		t.Fatalf("mismatch %v", err)
	}
}
