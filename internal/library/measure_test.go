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
	"strconv"
	"strings"
	"sync"
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
	smallPayloads := make([][]byte, smallCount)
	for i := range smallPayloads {
		smallPayloads[i] = make([]byte, smallBytes)
		if _, err := rand.Read(smallPayloads[i]); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	smallStart := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, smallCount)
	for i := 0; i < smallCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := lib.PutReader(ctx, fmt.Sprintf("small/file-%02d.bin", i), bytes.NewReader(smallPayloads[i]), int64(len(smallPayloads[i])))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	smallDuration := time.Since(smallStart)
	smallDisk, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	single, batches := lib.ResticCommitCounts()
	fmt.Printf("MEASURE small_files count=%d bytes=%d restic_commits=%d batch_commits=%d duration=%s repo_bytes=%d\n", smallCount, smallBytes*smallCount, single+batches, batches, smallDuration, smallDisk)
	singleBeforeLarge := single
	batchBeforeLarge := batches
	largeStart := time.Now()
	if _, err := lib.PutReader(ctx, "large/disk-image.iso", io.LimitReader(patternReader{}, largeBytes), largeBytes); err != nil {
		t.Fatal(err)
	}
	largeDuration := time.Since(largeStart)
	largeDisk, err := dirSize(filepath.Join(dir, "library"))
	if err != nil {
		t.Fatal(err)
	}
	single, batches = lib.ResticCommitCounts()
	runtime.ReadMemStats(&after)
	fmt.Printf("MEASURE large_file count=1 bytes=%d restic_commits=%d batch_commits=%d duration=%s repo_bytes_delta=%d\n", largeBytes, single-singleBeforeLarge, batches-batchBeforeLarge, largeDuration, largeDisk-smallDisk)
	fmt.Printf("MEASURE go_total_alloc_delta=%d heap_alloc_after=%d process_hwm_kb=%d staging_bytes_after=0\n", after.TotalAlloc-before.TotalAlloc, after.HeapAlloc, processHighWaterKB())
}

func processHighWaterKB() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		n, _ := strconv.ParseInt(fields[1], 10, 64)
		return n
	}
	return 0
}

type patternReader struct{}

func (patternReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i * 31)
	}
	return len(p), nil
}
