package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

const testPassword = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Deterministic data generated without buffering originals or staging plaintext.
type pattern struct{ offset uint64 }

func (p *pattern) Read(b []byte) (int, error) {
	for i := range b {
		x := p.offset + 0x9e3779b97f4a7c15
		x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
		x = (x ^ (x >> 27)) * 0x94d049bb133111eb
		b[i] = byte(x ^ (x >> 31))
		p.offset++
	}
	return len(b), nil
}

func data(size int64) io.Reader { return io.LimitReader(&pattern{}, size) }

func testEntry(i int, size int64) entry {
	h := sha256.New()
	_, _ = io.Copy(h, data(size))
	return entry{Name: fmt.Sprintf("%032x", i+1), Size: &size, SHA256: hex.EncodeToString(h.Sum(nil))}
}

func stock(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "restic", append([]string{"-r", repo}, args...)...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+testPassword)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("restic %v: %v: %s", args, err, out)
	}
	return out
}

func invoke(t *testing.T, binary, repo string, m manifest, lengths []int64, blocked int) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-repo", repo, "-workers", "2")
	b, _ := json.Marshal(m)
	cmd.Stdin = bytes.NewReader(b)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	var writes []*os.File
	for i := 0; i <= len(m.Files); i++ {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		cmd.ExtraFiles = append(cmd.ExtraFiles, r)
		writes = append(writes, w)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, r := range cmd.ExtraFiles {
		_ = r.Close()
	}
	done := make(chan struct{}, len(writes))
	ready := make(chan struct{})
	if blocked != -1 {
		// Wait for authentication and locking before interrupting stalled data.
		go func() {
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					locks, _ := os.ReadDir(filepath.Join(repo, "locks"))
					if len(locks) > 0 {
						time.Sleep(100 * time.Millisecond)
						if blocked == -2 {
							_ = cmd.Process.Signal(syscall.SIGTERM)
						}
						close(ready)
						return
					}
				}
			}
		}()
	}
	for i, w := range writes {
		if i > 0 && (i-1 == blocked || blocked == -2) {
			continue
		}
		go func(i int, w *os.File) {
			defer func() { _ = w.Close(); done <- struct{}{} }()
			if i == 0 {
				_, _ = io.WriteString(w, testPassword+"\n")
				return
			}
			if blocked >= 0 {
				select {
				case <-ready:
				case <-ctx.Done():
					return
				}
			}
			_, _ = io.Copy(w, data(lengths[i-1]))
		}(i, w)
	}
	err := cmd.Wait()
	for i := range writes {
		if i > 0 && (i-1 == blocked || blocked == -2) {
			continue
		}
		<-done
	}
	if ctx.Err() != nil {
		t.Fatal("writer hung, including on a failed source with blocked sibling")
	}
	return out.Bytes(), stderr.Bytes(), err
}

func TestStockResticIntegration(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("stock restic required")
	}
	dir := t.TempDir()
	binary, repo := filepath.Join(dir, "writer"), filepath.Join(dir, "repo")
	build := exec.Command("go", "build", "-race", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	stock(t, repo, "init", "--repository-version", "2")
	m := manifest{Version: 1}
	lengths := []int64{32<<20 + 7, 0, 1, 8192, 12345, 65536, 1 << 20, 2 << 20}
	for i, n := range lengths {
		m.Files = append(m.Files, testEntry(i, n))
	}
	out, stderr, err := invoke(t, binary, repo, m, lengths, -1)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("write: %v %s", err, stderr)
	}
	var result struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if json.Unmarshal(out, &result) != nil || !lowerHex(result.SnapshotID, 64) {
		t.Fatalf("invalid success: %s", out)
	}
	for _, e := range m.Files {
		cmd := exec.Command("restic", "-r", repo, "dump", result.SnapshotID, "/"+e.Name)
		cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+testPassword)
		h := sha256.New()
		counter := &countWriter{w: h}
		cmd.Stdout = counter
		cmd.Stderr = &bytes.Buffer{}
		if err := cmd.Run(); err != nil {
			t.Fatalf("dump: %v %s", err, cmd.Stderr)
		}
		if counter.n != *e.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
			t.Fatal("dump differs from input")
		}
	}
	stock(t, repo, "check", "--read-data")
	for _, tc := range []struct {
		name         string
		size, actual int64
		badHash      bool
		blocked      int
	}{
		{"short", 4096, 4095, false, -1},
		{"long", 4096, 4097, false, -1},
		{"checksum", 12<<20 + 3, 12<<20 + 3, true, -1},
		{"blocked-sibling", 4096, 0, false, 1},
		{"signal", 4096, 4096, false, -2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := manifest{Version: 1, Files: []entry{testEntry(20, tc.size), testEntry(21, 8192)}}
			if tc.badHash {
				bad.Files[0].SHA256 = strings.Repeat("0", 64)
			}
			out, stderr, err := invoke(t, binary, repo, bad, []int64{tc.actual, 8192}, tc.blocked)
			if err == nil || len(out) != 0 || string(stderr) != "batch write failed\n" {
				t.Fatalf("failure leaked success/details: %v %q %q", err, out, stderr)
			}
			var snapshots []json.RawMessage
			if json.Unmarshal(stock(t, repo, "snapshots", "--json"), &snapshots) != nil || len(snapshots) != 1 {
				t.Fatal("failed batch published a snapshot")
			}
		})
	}
	// A fresh invocation can commit a good sibling after an atomic batch failure.
	retry := manifest{Version: 1, Files: []entry{testEntry(21, 8192)}}
	if _, stderr, err := invoke(t, binary, repo, retry, []int64{8192}, -1); err != nil {
		t.Fatalf("retry: %v %s", err, stderr)
	}
	stock(t, repo, "check", "--read-data")
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
