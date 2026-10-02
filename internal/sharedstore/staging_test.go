package sharedstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func TestEncryptedStagingConfidentiality(t *testing.T) {
	root := testRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "shared-staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Store{root: root}
	for _, prefix := range []string{"source-", "manifest-"} {
		t.Run(prefix, func(t *testing.T) {
			marker := []byte(`{"key":"secret-manifest-key","source":"private-mobile-content"}`)
			data := bytes.Repeat(marker, 40000)
			stage, err := s.newEncryptedStage(prefix)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err = stage.Write(data); err != nil {
				t.Fatal(err)
			}
			check := func() []byte {
				t.Helper()
				disk, err := os.ReadFile(stage.file.Name())
				if err != nil {
					t.Fatal(err)
				}
				if len(disk) <= 1<<20 || !bytes.HasPrefix(disk, []byte("WZA1")) || bytes.Contains(disk, marker) {
					t.Fatal("unencrypted live staging")
				}
				info, err := os.Stat(stage.file.Name())
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("permissions: %v", err)
				}
				return disk
			}
			check() // Inspect flushed frames before finalization.
			reader, err := stage.Open(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			disk := check()
			got, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("roundtrip: %v", err)
			}
			disk[len(disk)-1] ^= 1
			if err = os.WriteFile(stage.file.Name(), disk, 0o600); err != nil {
				t.Fatal(err)
			}
			bad, err := cryptox.OpenStreamFile(context.Background(), stage.file.Name(), stage.key, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, bad)
			bad.Close()
			if !errors.Is(err, cryptox.ErrStreamFile) {
				t.Fatalf("tampering accepted: %v", err)
			}
			path, key := stage.file.Name(), stage.key
			stage.Close()
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("stage remains: %v", err)
			}
			if !bytes.Equal(key, make([]byte, 32)) {
				t.Fatal("transient key remains")
			}
		})
	}
}

func TestPrepareStagesContainOnlyCiphertext(t *testing.T) {
	root := testRoot(t)
	v := testVault(t, root, "owner")
	inspected := false
	s, err := Open(root, Options{FailureHook: func(point string) error {
		if point != "cipher_synced" {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(root, "shared-staging"))
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			body, err := os.ReadFile(filepath.Join(root, "shared-staging", entry.Name()))
			if err != nil {
				return err
			}
			if bytes.Contains(body, []byte("private-mobile-source-marker")) || bytes.Contains(body, []byte(`"type":"header"`)) {
				t.Fatal("plaintext in staging")
			}
			prefix := strings.Split(entry.Name(), "-")[0]
			switch prefix {
			case "source", "manifest":
				if !bytes.HasPrefix(body, []byte("WZA1")) {
					t.Fatal("unencrypted stage")
				}
			case "cipher":
				if !bytes.HasPrefix(body, objectMagic) {
					t.Fatal("object format changed")
				}
			default:
				t.Fatalf("unexpected stage: %s", entry.Name())
			}
			seen[prefix] = true
		}
		if !seen["source"] || !seen["manifest"] || !seen["cipher"] {
			t.Fatalf("missing stages: %v", seen)
		}
		inspected = true
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := bytes.Repeat([]byte("private-mobile-source-marker"), 100000)
	if _, err = s.Prepare(context.Background(), "owner", v, "entry", 1, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if !inspected {
		t.Fatal("hook not reached")
	}
	entries, err := os.ReadDir(filepath.Join(root, "shared-staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("stages remain: %v %v", entries, err)
	}
}

func TestPrepareSeekableEncryptedMobileInput(t *testing.T) {
	ctx := context.Background()
	root := testRoot(t)
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{FailureHook: func(point string) error {
		if point == "cipher_synced" {
			entries, err := os.ReadDir(filepath.Join(root, "shared-staging"))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "source-") {
					t.Fatal("seekable source staged")
				}
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := bytes.Repeat([]byte("encrypted-mobile-component"), 100000)
	stage, err := s.newEncryptedStage("component-")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err = stage.Write(append([]byte("prefix"), data...)); err != nil {
		t.Fatal(err)
	}
	input, err := stage.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := strings.Repeat("a", 32)
	prepare := func() (Prepared, error) {
		if _, err := input.Seek(6, io.SeekStart); err != nil {
			return Prepared{}, err
		}
		return s.PrepareSeekableWithID(ctx, op, "owner", v, "entry", 1, input, int64(len(data)))
	}
	p, err := prepare()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, op); err != nil {
		t.Fatal(err)
	}
	retry, err := prepare()
	if err != nil || retry != p {
		t.Fatalf("retry: %v", err)
	}
	var out bytes.Buffer
	if err = s.Read(ctx, "owner", v, p.Reference, &out); err != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("read: %v", err)
	}
	if _, err = s.PrepareSeekableWithID(ctx, op, "owner", v, "entry", 1, bytes.NewReader([]byte("changed")), 7); !errors.Is(err, ErrState) {
		t.Fatalf("changed retry: %v", err)
	}
}

type changingSeekReader struct{ *bytes.Reader }

func (r *changingSeekReader) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekStart {
		r.Reader = bytes.NewReader([]byte("new"))
	}
	return r.Reader.Seek(offset, whence)
}

func TestPrepareSeekableRejectsChangedPass(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := &changingSeekReader{bytes.NewReader([]byte("old"))}
	if _, err = s.PrepareSeekableWithID(ctx, strings.Repeat("b", 32), "owner", v, "entry", 1, input, 3); !errors.Is(err, ErrState) {
		t.Fatalf("changed source: %v", err)
	}
	pending, err := s.Pending(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("changed source journaled: %v %v", pending, err)
	}
}
