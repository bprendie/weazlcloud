package restic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

var ErrStreamsUnavailable = errors.New("Restic streaming writer is not installed")
var ErrStreamsFailed = errors.New("Restic streaming batch did not complete")

type StreamInput struct {
	Name   string    `json:"name"`
	Size   int64     `json:"size"`
	SHA256 string    `json:"sha256"`
	Reader io.Reader `json:"-"`
	// Abort optionally interrupts a blocked Read on cancellation/helper failure.
	// It must return promptly, be safe concurrently with Read, and tolerate an
	// already-completed read. It is called at most once and joined before return.
	// Do not supply Close for a reader whose mutable state is not concurrency-safe.
	Abort func() `json:"-"`
}

// PutStreams creates a stock Restic snapshot with independent, root-level files.
// Neither process spools originals to disk. All pipe writers are joined before
// returning, so a caller may safely close its encrypted source readers.
// Readers must finish cooperatively or provide Abort. The subprocess timeout
// does not bound joining an arbitrary blocked Reader. Production encrypted local
// file readers check their own contexts; this function cannot replace those
// contexts, interrupt a stalled filesystem read, or safely close such readers.
func PutStreams(parent context.Context, repo Repo, inputs []StreamInput) (snapshot string, resultErr error) {
	binary, err := exec.LookPath("weazl-restic-writer")
	if err != nil || runtime.GOOS == "windows" {
		return "", ErrStreamsUnavailable
	}
	if len(repo.Password) == 0 || len(repo.Password) > 128 || repo.Location == "" || len(inputs) < 1 || len(inputs) > 8 {
		return "", ErrStreamsFailed
	}
	total := int64(0)
	seen := map[string]bool{}
	for _, input := range inputs {
		if !streamHex(input.Name, 32) || !streamHex(input.SHA256, 64) || seen[input.Name] || input.Reader == nil || input.Size < 0 || input.Size > (64<<20)-total {
			return "", ErrStreamsFailed
		}
		seen[input.Name] = true
		total += input.Size
	}
	raw, err := json.Marshal(struct {
		Version int           `json:"version"`
		Files   []StreamInput `json:"files"`
	}{1, inputs})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	started := time.Now()
	defer func() {
		log.Printf("mobile storage batch files=%d bytes=%d duration_ms=%d success=%t", len(inputs), total, time.Since(started).Milliseconds(), resultErr == nil)
	}()
	var reads, writes []*os.File
	defer func() {
		for _, f := range reads {
			_ = f.Close()
		}
		for _, f := range writes {
			_ = f.Close()
		}
	}()
	for i := 0; i <= len(inputs); i++ {
		r, w, e := os.Pipe()
		if e != nil {
			return "", e
		}
		reads = append(reads, r)
		writes = append(writes, w)
	}
	cmd := exec.CommandContext(ctx, binary, "-repo", repo.Location, "-workers", "2")
	cmd.ExtraFiles = reads
	cmd.Stdin = bytes.NewReader(raw)
	// A soft heap target bounds index pressure without buffering original bytes.
	cmd.Env = append(cleanEnv(), "GOMEMLIMIT=536870912")
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &streamOutput{dst: &output, remaining: 4096}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return "", ErrStreamsFailed
	}
	for _, f := range reads {
		_ = f.Close()
	}
	for _, input := range inputs {
		if input.Abort != nil {
			finished := make(chan struct{})
			stop := context.AfterFunc(ctx, func() { defer close(finished); input.Abort() })
			defer func() {
				if !stop() {
					<-finished
				}
			}()
		}
	}
	done := make(chan error, len(inputs)+1)
	go func() {
		secret := []byte(hex.EncodeToString(repo.Password) + "\n")
		_, e := writes[0].Write(secret)
		clear(secret)
		_ = writes[0].Close()
		if e != nil {
			cancel()
		}
		done <- e
	}()
	for i, input := range inputs {
		go func(i int, input StreamInput) {
			hash := sha256.New()
			n, e := io.Copy(writes[i+1], io.TeeReader(io.LimitReader(input.Reader, input.Size+1), hash))
			if e == nil && (n != input.Size || hex.EncodeToString(hash.Sum(nil)) != input.SHA256) {
				e = ErrStreamsFailed
			}
			if e == nil {
				e = ctx.Err()
			}
			if e != nil {
				// Signal cancellation, but withhold EOF until the helper exits.
				// Exact valid bytes followed by a source error are not clean EOF.
				cancel()
			} else {
				_ = writes[i+1].Close()
			}
			done <- e
		}(i, input)
	}
	err = cmd.Wait()
	// Only after the helper exits is closing unverified streams safe. Its read
	// ends are now gone, so blocked writes also wake without forging source EOF.
	if err != nil {
		cancel()
	}
	for _, f := range writes {
		_ = f.Close()
	}
	for range len(inputs) + 1 {
		if e := <-done; e != nil && err == nil {
			err = e
		}
	}
	if parent.Err() != nil {
		return "", parent.Err()
	}
	if err != nil {
		return "", ErrStreamsFailed
	}
	var result struct {
		Snapshot string `json:"snapshot_id"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || !streamHex(result.Snapshot, 64) {
		return "", ErrStreamsFailed
	}
	return result.Snapshot, nil
}

func streamHex(s string, size int) bool {
	if len(s) != size {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type streamOutput struct {
	dst       io.Writer
	remaining int
}

func (w *streamOutput) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, ErrStreamsFailed
	}
	w.remaining -= len(p)
	return w.dst.Write(p)
}
