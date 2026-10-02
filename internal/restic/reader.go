package restic

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var ErrReader = errors.New("persistent metadata reader unavailable")

type ReaderResult struct {
	ID    uint64 `json:"id"`
	Body  []byte `json:"body,omitempty"`
	Size  uint64 `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
	Ready bool   `json:"ready,omitempty"`
}

// Reader keeps one authenticated index in a child for a single unlocked job.
// Requests carry only already-authorized immutable references. No plaintext is
// staged on disk. A shared Restic lock prevents pruning for the session lifetime.
type Reader struct {
	mu        sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	next      uint64
	pending   map[uint64]chan ReaderResult
	in        io.WriteCloser
	cancel    context.CancelFunc
	done      chan struct{}
	ready     chan struct{}
	slots     chan struct{}
	binary    bool
	streams   map[uint64]*readerStream
}

func StartReader(ctx context.Context, binary string, repo Repo, workers int, memory int64) (*Reader, error) {
	return startReader(ctx, binary, repo, workers, memory, 2)
}

func startReader(ctx context.Context, binary string, repo Repo, workers int, memory int64, protocol int) (*Reader, error) {
	if workers < 1 || workers > 16 || len(repo.Password) == 0 || memory < 128<<20 {
		return nil, ErrReader
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &Reader{pending: map[uint64]chan ReaderResult{}, streams: map[uint64]*readerStream{}, binary: protocol == 2, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), slots: make(chan struct{}, workers)}
	pr, pw, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	defer pr.Close()
	defer pw.Close()
	cmd := exec.CommandContext(ctx, binary, "-repo", repo.Location, "-workers", fmt.Sprint(workers), "-protocol", fmt.Sprint(protocol))
	cmd.ExtraFiles = []*os.File{pr}
	cmd.Env = append(cleanEnv(), fmt.Sprintf("GOMEMLIMIT=%d", memory))
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if r.in, err = cmd.StdinPipe(); err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		r.in.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		r.in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	go r.receive(cmd, out)
	secret := []byte(hex.EncodeToString(repo.Password) + "\n")
	_, err = pw.Write(secret)
	clear(secret)
	pw.Close()
	if err != nil {
		r.Close()
		return nil, ErrReader
	}
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-r.ready:
		return r, nil
	case <-r.done:
		r.Close()
		return nil, ErrReader
	case <-ctx.Done():
		r.Close()
		return nil, ctx.Err()
	case <-timer.C:
		r.Close()
		return nil, ErrReader
	}
}

func (r *Reader) receive(cmd *exec.Cmd, out io.Reader) {
	defer close(r.done)
	defer cmd.Wait()
	if r.binary {
		r.receiveStream(out)
		r.cancel()
		return
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 6<<20)
	ready := false
	for scan.Scan() {
		var result ReaderResult
		if json.Unmarshal(scan.Bytes(), &result) != nil || len(result.Body) > 4<<20 {
			break
		}
		if result.Ready {
			if ready {
				break
			}
			ready = true
			close(r.ready)
			continue
		}
		r.mu.Lock()
		ch := r.pending[result.ID]
		delete(r.pending, result.ID)
		if ch != nil {
			ch <- result
		} else {
			clear(result.Body)
		}
		r.mu.Unlock()
	}
	r.cancel()
}

func (r *Reader) Read(ctx context.Context, snapshot, object string, limit int) (ReaderResult, error) {
	if r.binary {
		if limit > 4<<20 {
			return ReaderResult{}, ErrReader
		}
		return r.ReadImage(ctx, snapshot, object, limit)
	}
	if limit < 0 || limit > 4<<20 || len(snapshot) != 64 || len(object) > 4096 {
		return ReaderResult{}, ErrReader
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return ReaderResult{}, ctx.Err()
	case <-r.done:
		return ReaderResult{}, ErrReader
	}
	defer func() { <-r.slots }()
	r.mu.Lock()
	r.next++
	id := r.next
	ch := make(chan ReaderResult, 1)
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
		select {
		case leftover := <-ch:
			clear(leftover.Body)
		default:
		}
	}()
	req := struct {
		ID       uint64 `json:"id"`
		Snapshot string `json:"snapshot"`
		Object   string `json:"object"`
		Limit    int    `json:"limit"`
	}{id, snapshot, object, limit}
	r.writeMu.Lock()
	err := json.NewEncoder(r.in).Encode(req)
	r.writeMu.Unlock()
	if err != nil {
		return ReaderResult{}, ErrReader
	}
	select {
	case result := <-ch:
		if result.Error != "" {
			clear(result.Body)
			return ReaderResult{}, ErrReader
		}
		return result, nil
	case <-ctx.Done():
		return ReaderResult{}, ctx.Err()
	case <-r.done:
		return ReaderResult{}, ErrReader
	}
}

func (r *Reader) Close() {
	r.closeOnce.Do(func() { r.in.Close(); r.cancel(); <-r.done })
}
