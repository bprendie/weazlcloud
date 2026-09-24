package takeout

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func fixture(t *testing.T, root, name string, entries map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for path, body := range entries {
		h := &zip.FileHeader{Name: path, Method: zip.Store}
		h.SetModTime(time.Date(2020, 5, 4, 12, 0, 0, 0, time.UTC))
		file, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationAndUnsafePaths(t *testing.T) {
	for source, want := range map[string]string{
		"Takeout/Drive/notes.txt":                        "Google Takeout/Drive/notes.txt",
		"Takeout/Google Photos/Photos from 2020/pic.jpg": "Google Takeout/Photos/Photos from 2020/pic.jpg",
		"Takeout/Calendar/cal.ics":                       "Google Takeout/Other/Calendar/cal.ics",
	} {
		got, err := Destination("Google Takeout", source)
		if err != nil || got != want {
			t.Fatalf("%q => %q, %v", source, got, err)
		}
	}
	for _, source := range []string{"../escape", "/root", "Takeout/Drive/../../escape", "Takeout\\Drive\\x", "C:/evil"} {
		if _, err := Destination("Google Takeout", source); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestImportRoutesAndResumesWithoutOverwrite(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	fixture(t, root, "part1.zip", map[string]string{
		"Takeout/Drive/note.txt":                              "hello drive",
		"Takeout/Google Photos/Photos from 2020/pic.jpg":      "photo data",
		"Takeout/Google Photos/Photos from 2020/pic.jpg.json": "sidecar",
	})
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("secret"), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	f, z, err := Open(root, "part1.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := Import(context.Background(), lib, "part1.zip", z, "Google Takeout", nil, nil)
	if err != nil || s.Imported != 3 {
		t.Fatalf("first import %+v: %v", s, err)
	}
	for path, want := range map[string]string{
		"Google Takeout/Drive/note.txt":                       "hello drive",
		"Google Takeout/Photos/Photos from 2020/pic.jpg":      "photo data",
		"Google Takeout/Photos/Photos from 2020/pic.jpg.json": "sidecar",
	} {
		got, err := lib.Get(context.Background(), path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	metadata, err := lib.Metadata(context.Background(), "Google Takeout/Drive/note.txt")
	if err != nil || metadata.Mtime.Year() != 2020 {
		t.Fatalf("mtime %+v %v", metadata, err)
	}
	s, err = Import(context.Background(), lib, "part1.zip", z, "Google Takeout", nil, nil)
	if err != nil || s.Imported != 0 || s.Skipped != 3 {
		t.Fatalf("resume %+v: %v", s, err)
	}
	fixture(t, root, "part2.zip", map[string]string{"Takeout/Drive/note.txt": "different"})
	f2, z2, err := Open(root, "part2.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	_, err = Import(context.Background(), lib, "part2.zip", z2, "Google Takeout", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("missing conflict: %v", err)
	}
}

func TestCorruptEntryDoesNotCommit(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	fixture(t, root, "broken.zip", map[string]string{"Takeout/Drive/file.txt": "unique-payload"})
	location := filepath.Join(root, "broken.zip")
	raw, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Replace(raw, []byte("unique-payload"), []byte("BROKEN-payload"), 1)
	if bytes.Equal(raw, corrupt) {
		t.Fatal("fixture payload not found")
	}
	if err := os.WriteFile(location, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("secret"), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	lib := library.New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	f, z, err := Open(root, "broken.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := Import(context.Background(), lib, "broken.zip", z, "Google Takeout", nil, nil); err == nil {
		t.Fatal("corrupt ZIP imported")
	}
	if _, err := lib.Metadata(context.Background(), "Google Takeout/Drive/file.txt"); err == nil {
		t.Fatal("corrupt file committed")
	}
}
