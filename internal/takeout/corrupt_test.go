package takeout

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestSkipCorruptRetainsGoodFilesAndReportsSource(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	fixture(t, root, "partial.zip", map[string]string{"Takeout/Drive/bad.txt": "unique-payload", "Takeout/Drive/good.txt": "keep this"})
	location := filepath.Join(root, "partial.zip")
	raw, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("unique-payload"), []byte("BROKEN-payload"), 1)
	if err = os.WriteFile(location, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err = v.Forge([]byte("secret"), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	f, z, err := Open(root, "partial.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for attempt := 0; attempt < 2; attempt++ {
		s, err := Import(context.Background(), lib, "partial.zip", z, "Google Takeout", nil, nil, Options{SkipCorrupt: true})
		if err != nil || s.Corrupt != 1 || s.Imported+s.Skipped != 1 || s.ProcessedBytes != s.Bytes || len(s.Errors) != 1 || s.Errors[0].Path != "Takeout/Drive/bad.txt" {
			t.Fatalf("%+v %v", s, err)
		}
		if _, err = lib.Metadata(context.Background(), "Google Takeout/Drive/bad.txt"); err == nil {
			t.Fatal("corrupt file committed")
		}
		good, err := lib.Get(context.Background(), "Google Takeout/Drive/good.txt")
		if err != nil || string(good) != "keep this" {
			t.Fatal("good file lost", err)
		}
	}
	fixture(t, root, "quota.zip", map[string]string{"Takeout/Drive/new.txt": "new good content"})
	f2, z2, err := Open(root, "quota.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	s, err := Import(context.Background(), lib, "quota.zip", z2, "Google Takeout", func(int64) (func(), error) { return nil, quota.ErrExceeded }, nil, Options{SkipCorrupt: true})
	if !errors.Is(err, quota.ErrExceeded) || s.Corrupt != 0 {
		t.Fatalf("storage failure was ignored: %+v %v", s, err)
	}
}
