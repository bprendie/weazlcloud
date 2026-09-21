package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestTransferMeasurement(t *testing.T) {
	if os.Getenv("WEAZLCLOUD_MEASURE") != "1" {
		t.Skip("set WEAZLCLOUD_MEASURE=1 to run the transfer measurement")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	if root := os.Getenv("WEAZLCLOUD_MEASURE_ROOT"); root != "" {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		measureDir, err := os.MkdirTemp(root, "weazl-measure-*")
		if err != nil {
			t.Fatal(err)
		}
		dir = measureDir
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("nug"), []byte("nug")); err != nil {
		t.Fatal(err)
	}
	lib := New(filepath.Join(dir, "library"), filepath.Join(dir, "catalog.enc"), v)
	ctx := context.Background()
	const smallCount = 24
	const smallBytes = int64(256 * 1024)
	largeBytes := smallBytes * smallCount
	small := make([]byte, smallBytes)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	smallStart := time.Now()
	for i := 0; i < smallCount; i++ {
		if _, err := rand.Read(small); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.PutReader(ctx, fmt.Sprintf("small/file-%02d.bin", i), bytes.NewReader(small), int64(len(small))); err != nil {
			t.Fatal(err)
		}
	}
	smallDuration := time.Since(smallStart)
	smallDisk, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	largeStart := time.Now()
	if _, err := lib.PutReader(ctx, "large/disk-image.iso", io.LimitReader(patternReader{}, largeBytes), largeBytes); err != nil {
		t.Fatal(err)
	}
	largeDuration := time.Since(largeStart)
	largeDisk, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	fmt.Printf("MEASURE small_files count=%d bytes=%d restic_commits=%d duration=%s repo_bytes=%d\n", smallCount, smallBytes*smallCount, smallCount, smallDuration, smallDisk)
	fmt.Printf("MEASURE large_file count=1 bytes=%d restic_commits=1 duration=%s repo_bytes_delta=%d\n", largeBytes, largeDuration, largeDisk-smallDisk)
	fmt.Printf("MEASURE go_total_alloc_delta=%d heap_alloc_after=%d staging_bytes_after=0\n", after.TotalAlloc-before.TotalAlloc, after.HeapAlloc)
}

type patternReader struct{}

func (patternReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i * 31)
	}
	return len(p), nil
}
