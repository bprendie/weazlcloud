package restic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamingWriterRoundTripAndRejectedInput(t *testing.T) {
	for _, binary := range []string{"restic", "weazl-restic-writer"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(binary + " unavailable")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := Repo{Location: filepath.Join(t.TempDir(), "repo"), Password: []byte("streaming-test-password")}
	runner := New()
	if err := runner.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	bodies := [][]byte{bytes.Repeat([]byte("one-original"), 1000), bytes.Repeat([]byte("second-original"), 1200)}
	inputs := make([]StreamInput, len(bodies))
	for i, body := range bodies {
		sum := sha256.Sum256(body)
		inputs[i] = StreamInput{Name: strings.Repeat(string(rune('a'+i)), 32), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Reader: bytes.NewReader(body)}
	}
	id, err := PutStreams(ctx, repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	for i, input := range inputs {
		var got bytes.Buffer
		if err = runner.Dump(ctx, repo, id, input.Name, &got); err != nil || !bytes.Equal(got.Bytes(), bodies[i]) {
			t.Fatal("ordinary Restic dump changed source", err)
		}
	}
	snapshots, err := runner.Snapshots(ctx, repo)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("batch created %d snapshots: %v", len(snapshots), err)
	}
	for _, bad := range []string{"short", "long", "hash", "terminal-error"} {
		t.Run(bad, func(t *testing.T) {
			data := append([]byte(nil), bodies[0]...)
			if bad == "short" {
				data = data[:len(data)-1]
			}
			if bad == "long" {
				data = append(data, '!')
			}
			if bad == "hash" {
				data[0] ^= 1
			}
			input := inputs[0]
			input.Reader = bytes.NewReader(data)
			if bad == "terminal-error" {
				input.Reader = io.MultiReader(input.Reader, terminalStreamError{})
			}
			if _, err := PutStreams(ctx, repo, []StreamInput{input}); !errors.Is(err, ErrStreamsFailed) {
				t.Fatal("invalid source accepted", err)
			}
			snapshots, err := runner.Snapshots(ctx, repo)
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("invalid snapshot published: %d %v", len(snapshots), err)
			}
		})
	}
}

func TestStreamingWriterCancellationReapsPipes(t *testing.T) {
	for _, binary := range []string{"restic", "weazl-restic-writer"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(binary + " unavailable")
		}
	}
	repo := Repo{Location: filepath.Join(t.TempDir(), "repo"), Password: []byte("cancel-writer")}
	if err := New().Init(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	aborted := make(chan struct{})
	input := StreamInput{Name: strings.Repeat("a", 32), Size: 1, SHA256: strings.Repeat("0", 64), Reader: r,
		Abort: func() { _ = r.CloseWithError(context.Canceled); close(aborted) },
	}
	start := time.Now()
	_, err := PutStreams(ctx, repo, []StreamInput{input})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 7*time.Second {
		t.Fatal("cancellation did not reap writer", err)
	}
	select {
	case <-aborted:
	default:
		t.Fatal("Abort was not joined")
	}
}

type terminalStreamError struct{}

func (terminalStreamError) Read([]byte) (int, error) { return 0, errors.New("source terminal error") }

// Delay signal forwarding so a premature pipe close deterministically gives
// the real helper time to publish. Correct code withholds EOF during this delay.
func TestStreamingWriterTerminalErrorWithDelayedSignal(t *testing.T) {
	helper, err := exec.LookPath("weazl-restic-writer")
	if err != nil {
		t.Skip("weazl-restic-writer unavailable")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	helper, err = filepath.Abs(helper)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STREAM_TEST_HELPER", helper)
	wrapper := "#!/bin/sh\ntrap 'sleep 2; kill -TERM \"$child\" 2>/dev/null; wait \"$child\"; exit 1' TERM\n\"$STREAM_TEST_HELPER\" \"$@\" <&0 &\nchild=$!\nwait \"$child\"\n"
	if err := os.WriteFile(filepath.Join(dir, "weazl-restic-writer"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := Repo{Location: filepath.Join(dir, "repo"), Password: []byte("terminal-error-test")}
	runner := New()
	if err := runner.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("valid-source"), 8192)
	sum := sha256.Sum256(body)
	input := StreamInput{Name: strings.Repeat("a", 32), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		Reader: io.MultiReader(bytes.NewReader(body), terminalStreamError{}),
	}
	if id, err := PutStreams(ctx, repo, []StreamInput{input}); id != "" || !errors.Is(err, ErrStreamsFailed) {
		t.Fatalf("source error accepted: %q %v", id, err)
	}
	snapshots, err := runner.Snapshots(ctx, repo)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("source error published %d snapshots: %v", len(snapshots), err)
	}
}

func TestStreamingWriterUnavailableDoesNotRead(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := PutStreams(context.Background(), Repo{}, []StreamInput{{Reader: unexpectedStreamRead{t}}})
	if !errors.Is(err, ErrStreamsUnavailable) {
		t.Fatal(err)
	}
}

type unexpectedStreamRead struct{ t *testing.T }

func (r unexpectedStreamRead) Read([]byte) (int, error) {
	r.t.Error("unavailable writer consumed source")
	return 0, io.EOF
}
