package takeout

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func parallelLibrary(t *testing.T) *library.Library {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("secret"), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	return library.New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
}

type zipEntry struct{ name, body string }

func orderedZIP(t *testing.T, entries ...zipEntry) *zip.Reader {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, entry := range entries {
		f, err := w.Create("Takeout/Drive/" + entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func TestSmallEntriesUseBatchesAndResume(t *testing.T) {
	lib := parallelLibrary(t)
	var entries []zipEntry
	for i := 0; i < 16; i++ {
		entries = append(entries, zipEntry{fmt.Sprintf("file-%02d.txt", i), fmt.Sprintf("body %d", i)})
	}
	z := orderedZIP(t, entries...)
	last := 0
	progress := func(s Summary) {
		if s.Imported+s.Skipped < last {
			t.Error("progress moved backwards")
		}
		last = s.Imported + s.Skipped
	}
	s, err := Import(context.Background(), lib, "batch.zip", z, "Google Takeout", nil, progress)
	if err != nil || s.Imported != len(entries) || s.ProcessedBytes != s.Bytes || last != len(entries) {
		t.Fatalf("%+v %v", s, err)
	}
	singles, batches := lib.ResticCommitCounts()
	if batches == 0 || singles+batches >= uint64(len(entries)) {
		t.Fatalf("small files were not batched: singles=%d batches=%d", singles, batches)
	}
	for _, entry := range entries {
		got, err := lib.Get(context.Background(), "Google Takeout/Drive/"+entry.name)
		if err != nil || string(got) != entry.body {
			t.Fatalf("readback %s: %q %v", entry.name, got, err)
		}
	}
	s, err = Import(context.Background(), lib, "batch.zip", z, "Google Takeout", nil, nil)
	if err != nil || s.Imported != 0 || s.Skipped != len(entries) || s.ProcessedBytes != s.Bytes {
		t.Fatalf("resume %+v %v", s, err)
	}
}

func TestDuplicateAndParentPathsRemainOrdered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []zipEntry
		fail    bool
		skipped int
	}{
		{"identical", []zipEntry{{"a", "same"}, {"a", "same"}}, false, 1},
		{"conflicting", []zipEntry{{"a", "same"}, {"a", "evil"}}, true, 0},
		{"parent", []zipEntry{{"a", "same"}, {"a/child", "evil"}}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := parallelLibrary(t)
			s, err := Import(context.Background(), lib, "paths.zip", orderedZIP(t, tc.entries...), "Google Takeout", nil, nil)
			if (err != nil) != tc.fail || s.Imported != 1 || s.Skipped != tc.skipped {
				t.Fatalf("%+v %v", s, err)
			}
			got, err := lib.Get(context.Background(), "Google Takeout/Drive/a")
			if err != nil || string(got) != "same" {
				t.Fatalf("original overwritten: %q %v", got, err)
			}
		})
	}
}

func TestLargeEntryStreamsAloneAndReservationsRelease(t *testing.T) {
	lib := parallelLibrary(t)
	z := orderedZIP(t, zipEntry{"a", "small"}, zipEntry{"b", "small"}, zipEntry{"large", strings.Repeat("z", parallelEntryLimit+1)}, zipEntry{"c", "small"}, zipEntry{"d", "small"})
	var mu sync.Mutex
	active, peak := 0, 0
	largeActive := false
	reserve := func(size int64) (func(), error) {
		mu.Lock()
		large := size > 2*parallelEntryLimit+16<<20
		if largeActive || (large && active != 0) {
			t.Error("large entry overlapped another entry")
		}
		largeActive = large
		active++
		peak = max(peak, active)
		mu.Unlock()
		return func() {
			mu.Lock()
			active--
			if large {
				largeActive = false
			}
			mu.Unlock()
		}, nil
	}
	s, err := Import(context.Background(), lib, "large.zip", z, "Google Takeout", reserve, nil)
	if err != nil || s.Imported != 5 || s.ProcessedBytes != s.Bytes || active != 0 || peak > importWorkers {
		t.Fatalf("%+v %v active=%d peak=%d", s, err, active, peak)
	}
}

func TestFailedWaveAccountsCommittedPeersAndStopsScheduling(t *testing.T) {
	lib := parallelLibrary(t)
	entries := []zipEntry{{"bad", "quota-error"}}
	for i := 1; i < 9; i++ {
		entries = append(entries, zipEntry{fmt.Sprintf("good-%d", i), "good"})
	}
	reserve := func(size int64) (func(), error) {
		if size == int64(len("quota-error"))*2+16<<20 {
			return nil, quota.ErrExceeded
		}
		return func() {}, nil
	}
	s, err := Import(context.Background(), lib, "failed.zip", orderedZIP(t, entries...), "Google Takeout", reserve, nil, Options{SkipCorrupt: true})
	if !errors.Is(err, quota.ErrExceeded) || s.Imported != 7 || s.Corrupt != 0 || s.ProcessedBytes != 28 {
		t.Fatalf("wave failure accounting: %+v %v", s, err)
	}
	for _, name := range []string{"bad", "good-8"} {
		if _, err := lib.Metadata(context.Background(), "Google Takeout/Drive/"+name); err == nil {
			t.Fatalf("unexpected commit: %s", name)
		}
	}
	s, err = Import(context.Background(), lib, "failed.zip", orderedZIP(t, entries...), "Google Takeout", nil, nil)
	if err != nil || s.Imported != 2 || s.Skipped != 7 || s.ProcessedBytes != s.Bytes {
		t.Fatalf("resume after failure: %+v %v", s, err)
	}
}
