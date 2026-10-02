package restic

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistentReaderParityParallelFailureAndRestart(t *testing.T) {
	binary, err := exec.LookPath("weazl-restic-reader")
	if err != nil {
		t.Skip("build native/restic-reader and add it to PATH")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	repo := Repo{Location: filepath.Join(t.TempDir(), "repo"), Password: []byte("reader-private-test-key")}
	ctx := context.Background()
	runner := New()
	if err = runner.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("test authenticated chunks\n"), 400000)
	snapshot, err := runner.Put(ctx, repo, "nested/photo.jpg", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r, err := StartReader(ctx, binary, repo, 4, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limit := 1024 + i
			got, e := r.Read(ctx, snapshot, "/nested/photo.jpg", limit)
			if e != nil || got.Size != uint64(len(body)) || !bytes.Equal(got.Body, body[:limit]) {
				t.Errorf("prefix mismatch: %v", e)
			}
		}(i)
	}
	wg.Wait()
	if _, err = r.Read(ctx, snapshot, "/missing", 10); err == nil {
		t.Fatal("missing source succeeded")
	}
	good, err := r.Read(ctx, snapshot, "/nested/photo.jpg", 32)
	if err != nil || !bytes.Equal(good.Body, body[:32]) {
		t.Fatal("one error killed reader", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _ = r.Read(canceled, snapshot, "/nested/photo.jpg", 1)
	r.Close()
	// Graceful close removes its read lock; an exclusive writer must work.
	if err = runner.Run(ctx, repo, nil, nil, "check"); err != nil {
		t.Fatal("reader leaked a repository lock", err)
	}
	// An index loaded before a new snapshot cannot read its new blobs. A new
	// session can; callers must fall back or reopen instead of serving old data.
	newBody := []byte("new metadata")
	newSnap, err := runner.Put(ctx, repo, "new.json", bytes.NewReader(newBody))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := StartReader(ctx, binary, repo, 1, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r2.Read(ctx, newSnap, "new.json", len(newBody))
	if err != nil || !bytes.Equal(got.Body, newBody) {
		t.Fatal("restart missed new source", err)
	}
	r2.Close()
	files, _ := os.ReadDir(filepath.Join(repo.Location, "locks"))
	if len(files) != 0 {
		t.Fatal("locks remain")
	}
}

func TestPersistentReaderCanceledStartup(t *testing.T) {
	binary, err := exec.LookPath("weazl-restic-reader")
	if err != nil {
		t.Skip("metadata reader unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	_, err = StartReader(ctx, binary, Repo{Location: t.TempDir(), Password: []byte("secret")}, 1, 256<<20)
	if err == nil {
		t.Fatal("canceled startup succeeded")
	}
}
