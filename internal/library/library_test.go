package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestPutGetDeleteAndDedupe(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	payload := make([]byte, 120000)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Put(ctx, "Documents/a.txt", payload); err != nil {
		t.Fatal(err)
	}
	first, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Put(ctx, "Documents/b.txt", payload); err != nil {
		t.Fatal(err)
	}
	second, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	if second-first > int64(len(payload))/2 {
		t.Fatalf("dedupe failed: first=%d second=%d delta=%d payload=%d", first, second, second-first, len(payload))
	}
	got, err := lib.Get(ctx, "Documents/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("get mismatch")
	}
	var streamed bytes.Buffer
	if err := lib.StreamTo(ctx, "Documents/a.txt", &streamed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(streamed.Bytes(), payload) {
		t.Fatal("stream mismatch")
	}
	if err := lib.Delete("Documents/a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Get(ctx, "Documents/a.txt"); err == nil {
		t.Fatal("deleted file still present")
	}
	list := lib.List()
	if len(list) != 1 || list[0].Path != "Documents/b.txt" {
		t.Fatalf("list %+v", list)
	}
}

func TestRejectsTraversal(t *testing.T) {
	for _, p := range []string{"/etc/passwd", "../x", "foo/../../x", ""} {
		if _, err := cleanPath(p); err == nil {
			t.Fatalf("allowed %q", p)
		}
	}
}

func TestPutGetPNG(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Put(context.Background(), "Pictures/pixel.png", png); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Get(context.Background(), "Pictures/pixel.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, png) {
		t.Fatal("png round trip mismatch")
	}
}

func TestStagedUploadRecoversOnNextEnsure(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	payload := bytes.Repeat([]byte("recover-me"), 10000)
	stage, err := lib.stageReader("recovered.iso", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	lib.setStageActive(stage.ID, false)
	if err := lib.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Get(context.Background(), "recovered.iso")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("recovered upload err=%v size=%d", err, len(got))
	}
	if entries, err := os.ReadDir(lib.stageDir()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("staging files remain: %v", entries)
	}
}

func TestBatchCommitRestoresEachPath(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	first := []byte("first batch payload")
	second := []byte("second batch payload")
	one, err := lib.stageReader("folder/one.txt", bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	two, err := lib.stageReader("folder/two.txt", bytes.NewReader(second), int64(len(second)))
	if err != nil {
		t.Fatal(err)
	}
	lib.setStageActive(one.ID, false)
	lib.setStageActive(two.ID, false)
	lib.mu.Lock()
	if err := lib.ensure(context.Background()); err != nil {
		lib.mu.Unlock()
		t.Fatal(err)
	}
	err = lib.commitStagedBatch(context.Background(), []batchRequest{{stage: one}, {stage: two}})
	lib.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	gotOne, err := lib.Get(context.Background(), one.Path)
	if err != nil || !bytes.Equal(gotOne, first) {
		t.Fatalf("first batch file err=%v body=%q", err, gotOne)
	}
	gotTwo, err := lib.Get(context.Background(), two.Path)
	if err != nil || !bytes.Equal(gotTwo, second) {
		t.Fatalf("second batch file err=%v body=%q", err, gotTwo)
	}
	_, batches := lib.ResticCommitCounts()
	if batches != 1 {
		t.Fatalf("batch commits=%d", batches)
	}
}

func TestLockedPut(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	if _, err := lib.Put(context.Background(), "a.txt", []byte("x")); err != vault.ErrLocked {
		t.Fatalf("locked put %v", err)
	}
}

func dirSize(root string) (int64, error) {
	var n int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n, err
}
