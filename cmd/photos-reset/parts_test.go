package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestResetPartsPreservesFileBackups(t *testing.T) {
	root, backup := t.TempDir(), t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("test-pass"), []byte("test-pass")); err != nil {
		t.Fatal(err)
	}
	defer v.Lock()
	photo, file := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, item := range []struct{ id, kind string }{{photo, "photo"}, {file, "file"}} {
		raw, _ := json.Marshal(map[string]any{"id": item.id, "owner_id": "owner", "spec": map[string]string{"kind": item.kind}})
		sealed, err := v.Wrap(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, base := range []string{root, backup} {
			dir := filepath.Join(base, ".weazl-mobile-parts", item.id)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "session.enc"), sealed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "original-0.part"), []byte("encrypted payload"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, index := range []string{".live", ".queue"} {
			dir := filepath.Join(root, ".weazl-mobile-parts", index)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, item.id), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := resetPhotoParts(root, backup, "wrong-owner", v, false); err == nil {
		t.Fatal("accepted wrong owner")
	}
	if err := resetPhotoParts(root, t.TempDir(), "owner", v, true); err == nil {
		t.Fatal("accepted missing rollback")
	}
	if err := resetPhotoParts(root, backup, "owner", v, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".weazl-mobile-parts", photo, "session.enc")); err != nil {
		t.Fatal("inspection mutated state")
	}
	if err := resetPhotoParts(root, backup, "owner", v, true); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{photo, filepath.Join(".live", photo), filepath.Join(".queue", photo)} {
		if _, err := os.Stat(filepath.Join(root, ".weazl-mobile-parts", suffix)); !os.IsNotExist(err) {
			t.Fatal("photo state retained", err)
		}
	}
	for _, suffix := range []string{filepath.Join(file, "session.enc"), filepath.Join(file, "original-0.part"), filepath.Join(".live", file), filepath.Join(".queue", file)} {
		if _, err := os.Stat(filepath.Join(root, ".weazl-mobile-parts", suffix)); err != nil {
			t.Fatal("file state lost", err)
		}
	}
}
