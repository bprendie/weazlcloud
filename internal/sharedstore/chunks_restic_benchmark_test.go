package sharedstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type treeMeasurement struct{ allocated, apparent int64 }

func findBenchmarkReference(t *testing.T, store *Store, owner, entry string) Reference {
	t.Helper()
	var id, op, state string
	var revision uint64
	err := store.db.QueryRow(`SELECT object_id,op_id,state,revision FROM owners WHERE owner_id=? AND entry_id=?`, store.keys.ownerToken(owner), entry).Scan(&id, &op, &state, &revision)
	if err != nil || state != "live" {
		t.Fatalf("missing benchmark reference: %v", err)
	}
	return Reference{Version: chunkFormatVersion, ObjectID: id, EntryID: entry, Revision: revision, Operation: op}
}

func runResticComparison(t *testing.T, root, fixtureRoot string, files map[string]benchmarkFixture, workload map[string][]string) (treeMeasurement, time.Duration, time.Duration, time.Duration, int64) {
	t.Helper()
	resticRoot := filepath.Join(root, "restic-repos")
	if err := os.MkdirAll(resticRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	password := "d3-disposable-repository-pass"
	var writeDuration, readDuration, cpu time.Duration
	var maxRSS int64
	for _, owner := range []string{"alice", "bob"} {
		paths := workload[owner]
		repo := filepath.Join(resticRoot, owner)
		staging := filepath.Join(root, "restic-source", owner)
		if err := os.MkdirAll(staging, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			copyFixture(t, filepath.Join(fixtureRoot, path), filepath.Join(staging, path))
		}
		_, duration, childCPU, rss := runCommand(t, password, "-r", repo, "init")
		writeDuration += duration
		cpu += childCPU
		maxRSS = max(maxRSS, rss)
		_, duration, childCPU, rss = runCommand(t, password, "-r", repo, "backup", "--host", owner, "--no-scan", staging)
		writeDuration += duration
		cpu += childCPU
		maxRSS = max(maxRSS, rss)
		target := filepath.Join(root, "restic-restored", owner)
		_, duration, childCPU, rss = runCommand(t, password, "-r", repo, "restore", "latest", "--target", target)
		readDuration += duration
		cpu += childCPU
		maxRSS = max(maxRSS, rss)
		for _, path := range paths {
			found := findSuffixFile(t, target, path)
			if hash, err := hashFile(found); err != nil || hash != files[path].SHA256 {
				t.Fatalf("Restic restored hash mismatch for %q: %v", path, err)
			}
		}
	}
	return measureTree(t, resticRoot), writeDuration, readDuration, cpu, maxRSS
}

func runCommand(t *testing.T, password string, args ...string) ([]byte, time.Duration, time.Duration, int64) {
	t.Helper()
	cmd := exec.Command("restic", args...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password, "RESTIC_CACHE_DIR="+filepath.Join(os.TempDir(), "weazl-d3-restic-cache"))
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("restic %s: %v: %s", strings.Join(args, " "), err, output.String())
	}
	var cpu time.Duration
	var rss int64
	if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		cpu = time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
		rss = usage.Maxrss
	}
	return output.Bytes(), duration, cpu, rss
}

func copyFixture(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func findSuffixFile(t *testing.T, root, suffix string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil && (filepath.ToSlash(rel) == suffix || strings.HasSuffix(filepath.ToSlash(rel), "/"+suffix)) {
			found = path
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("Restic restore is missing %q: %v", suffix, err)
	}
	return found
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func measureTree(t *testing.T, root string) treeMeasurement {
	t.Helper()
	var result treeMeasurement
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		result.apparent += info.Size()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			result.allocated += stat.Blocks * 512
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func processHWMKiB() int64 {
	data, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			var value int64
			_, _ = fmt.Sscanf(line, "VmHWM: %d", &value)
			return value
		}
	}
	return 0
}

func commandOutput(name string, args ...string) string {
	data, _ := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(data))
}
