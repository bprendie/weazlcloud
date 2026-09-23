package sharedstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

type benchmarkFixture struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func TestChunkDedupeBaseline(t *testing.T) {
	if os.Getenv("WEAZLCLOUD_CHUNK_BASELINE") != "1" {
		t.Skip("run through scripts/dedupe-baseline.sh on a disposable volume")
	}
	root, fixtureRoot := os.Getenv("WEAZLCLOUD_BASELINE_ROOT"), os.Getenv("WEAZLCLOUD_DEDUPE_FIXTURES")
	if root == "" || fixtureRoot == "" {
		t.Fatal("baseline root and fixture directory are required")
	}
	var fixtureManifest struct {
		Files []benchmarkFixture `json:"files"`
	}
	manifest, err := os.ReadFile(filepath.Join(fixtureRoot, "manifest.json"))
	if err != nil || json.Unmarshal(manifest, &fixtureManifest) != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	files := map[string]benchmarkFixture{}
	for _, file := range fixtureManifest.Files {
		files[file.Path] = file
	}
	workload := map[string][]string{
		"alice": {"shared/identical.bin", "unique/alice.bin", "repeated/repeated-chunks.bin", "disk/disk-image-v1.img", "disk/disk-image-v2.img"},
		"bob":   {"shared/identical.bin", "unique/bob.bin"},
	}
	sharedRoot := filepath.Join(root, "chunk-store")
	store, err := Open(sharedRoot, Options{})
	if err != nil {
		t.Fatal(err)
	}
	vaults := map[string]*vault.Vault{"alice": testVault(t, root, "bench-alice"), "bob": testVault(t, root, "bench-bob")}
	var logical int64
	start := time.Now()
	var before, after runtime.MemStats
	var cpuBefore, cpuAfter syscall.Rusage
	runtime.ReadMemStats(&before)
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore)
	for _, owner := range []string{"alice", "bob"} {
		paths := workload[owner]
		for _, path := range paths {
			f, openErr := os.Open(filepath.Join(fixtureRoot, path))
			if openErr != nil {
				t.Fatal(openErr)
			}
			entry := strings.ReplaceAll(owner+"/"+path, "/", "_")
			p, putErr := store.Prepare(context.Background(), owner, vaults[owner], entry, 1, f, files[path].Size)
			_ = f.Close()
			if putErr == nil {
				putErr = store.MarkPublished(context.Background(), p.Operation)
			}
			if putErr == nil {
				putErr = store.Commit(context.Background(), p.Operation)
			}
			if putErr != nil {
				t.Fatal(putErr)
			}
			logical += files[path].Size
		}
	}
	writeDuration := time.Since(start)
	runtime.ReadMemStats(&after)
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter)
	stats, err := store.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.LogicalBytes != logical || stats.UniqueBytes > logical {
		t.Fatalf("shared chunk metrics logical=%d unique=%d fixture logical=%d", stats.LogicalBytes, stats.UniqueBytes, logical)
	}
	chunkAllocated := objectKindAllocated(t, store, "chunk")
	start = time.Now()
	for _, owner := range []string{"alice", "bob"} {
		paths := workload[owner]
		for _, path := range paths {
			entry := strings.ReplaceAll(owner+"/"+path, "/", "_")
			ref := findBenchmarkReference(t, store, owner, entry)
			h := sha256.New()
			if err = store.Read(context.Background(), owner, vaults[owner], ref, h); err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(h.Sum(nil)) != files[path].SHA256 {
				t.Fatalf("shared readback hash mismatch for fixture %q", path)
			}
		}
	}
	readDuration := time.Since(start)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	sharedSpace := measureTree(t, sharedRoot)
	resticSpace, resticWrite, resticRead, resticCPU, resticRSS := runResticComparison(t, root, fixtureRoot, files, workload)
	fmt.Printf("D3_COMPARE logical_bytes=%d shared_dedupe_savings_bytes=%d shared_unique_chunk_bytes=%d shared_chunk_allocated_bytes=%d shared_manifest_index_allocated_bytes=%d shared_allocated_bytes=%d shared_objects=%d shared_write_ms=%d shared_read_ms=%d shared_cpu_ms=%d shared_alloc_delta=%d shared_go_hwm_kib=%d\n", logical, logical-stats.UniqueBytes, stats.UniqueBytes, chunkAllocated, sharedSpace.allocated-chunkAllocated, sharedSpace.allocated, stats.Objects, writeDuration.Milliseconds(), readDuration.Milliseconds(), cpuDuration(cpuAfter)-cpuDuration(cpuBefore), after.TotalAlloc-before.TotalAlloc, processHWMKiB())
	fmt.Printf("D3_COMPARE restic_version=%s restic_allocated_bytes=%d restic_write_ms=%d restic_restore_ms=%d restic_child_cpu_ms=%d restic_child_maxrss_kib=%d\n", commandOutput("restic", "version"), resticSpace.allocated, resticWrite.Milliseconds(), resticRead.Milliseconds(), resticCPU.Milliseconds(), resticRSS)
	fmt.Printf("D3_COMPARE interpretation=shared_unique_counts_plaintext_chunks_restic_allocation_includes_compression_and_metadata_shared_allocation_includes_encrypted_manifests_and_sqlite\n")
}

func objectKindAllocated(t *testing.T, store *Store, kind string) int64 {
	t.Helper()
	rows, err := store.db.Query("SELECT object_id FROM objects WHERE kind=? AND state='ready'", kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		info, statErr := os.Stat(store.objectPath(id))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			total += stat.Blocks * 512
		} else {
			total += info.Size()
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return total
}

func cpuDuration(r syscall.Rusage) time.Duration {
	return time.Duration(r.Utime.Sec+r.Stime.Sec)*time.Second + time.Duration(r.Utime.Usec+r.Stime.Usec)*time.Microsecond
}
