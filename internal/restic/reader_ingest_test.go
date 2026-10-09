package restic

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestResidentReaderRefreshesDuringIngest(t *testing.T) {
	binary, err := exec.LookPath("weazl-restic-reader")
	if err != nil {
		t.Skip("metadata reader unavailable")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	ctx := context.Background()
	repo := Repo{Location: filepath.Join(t.TempDir(), "repo"), Password: []byte("disposable-reader-key")}
	runner := New()
	if err := runner.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	oldBody := bytes.Repeat([]byte("old photo"), 10000)
	oldSnapshot, err := runner.Put(ctx, repo, "old.jpg", bytes.NewReader(oldBody))
	if err != nil {
		t.Fatal(err)
	}
	readers := []*Reader{}
	for _, protocol := range []int{2, 1} {
		r, err := startReader(ctx, binary, repo, 4, 256<<20, protocol)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		readers = append(readers, r)
	}
	for pass := 0; pass < 2; pass++ {
		body := bytes.Repeat([]byte{byte(pass + 1)}, 200000)
		snapshot, err := runner.Put(ctx, repo, "new.jpg", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, reader := range readers {
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int, r *Reader) {
					defer wg.Done()
					snap, name, want := snapshot, "new.jpg", body
					if i%2 == 0 {
						snap, name, want = oldSnapshot, "old.jpg", oldBody
					}
					read := r.ReadImage
					if !r.binary {
						read = r.Read
					}
					got, err := read(ctx, snap, name, len(want))
					if err != nil || got.Size != uint64(len(want)) || !bytes.Equal(got.Body, want) {
						t.Errorf("resident read pass=%d old=%t: %v", pass, i%2 == 0, err)
					}
				}(i, reader)
			}
		}
		wg.Wait()
	}
}
