package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/repository"
)

func main() {
	flags := flag.NewFlagSet("restic-writer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	location := flags.String("repo", "", "local repository")
	workers := flags.Int("workers", 2, "file read concurrency (1-2)")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 || *location == "" || *workers < 1 || *workers > 2 {
		fmt.Fprintln(os.Stderr, errBatch)
		os.Exit(2)
	}
	runtime.GOMAXPROCS(2)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if run(ctx, *location, *workers) != nil {
		fmt.Fprintln(os.Stderr, errBatch)
		os.Exit(1)
	}
}

func run(parent context.Context, location string, workers int) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	passwordFD := inheritedFile(3)
	if passwordFD == nil {
		return errBatch
	}
	defer passwordFD.Close()
	input := inheritedFile(0)
	if input == nil {
		return errBatch
	}
	defer input.Close()
	stopInput := context.AfterFunc(ctx, func() { _ = passwordFD.Close(); _ = input.Close() })
	defer stopInput()
	password, err := io.ReadAll(io.LimitReader(passwordFD, 258))
	defer clear(password)
	if err != nil || len(password) > 257 {
		return errBatch
	}
	pass := strings.TrimSpace(string(password))
	if len(pass) == 0 || len(pass) > 256 || len(pass)%2 != 0 || !lowerHex(pass, len(pass)) {
		return errBatch
	}
	_ = passwordFD.Close()
	m, err := readManifest(input)
	if err != nil {
		return errBatch
	}
	pipes := make([]*os.File, 0, len(m.Files))
	defer func() {
		for _, p := range pipes {
			_ = p.Close()
		}
	}()
	for i := range m.Files {
		p := inheritedFile(4 + i)
		if p == nil {
			return errBatch
		}
		pipes = append(pipes, p)
		info, err := p.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return errBatch
		}
	}
	stopPipes := context.AfterFunc(ctx, func() {
		for _, p := range pipes {
			_ = p.Close()
		}
	})
	defer stopPipes()
	cfg := local.NewConfig()
	cfg.Path, cfg.Connections = location, 2
	backend, err := local.Open(ctx, cfg)
	if err != nil {
		return errBatch
	}
	defer backend.Close()
	repo, err := repository.New(backend, repository.Options{})
	if err != nil {
		return errBatch
	}
	if repo.SearchKey(ctx, pass, 20, "") != nil {
		return errBatch
	}
	clear(password)
	pass = ""
	unlock, lockedCtx, err := repository.Lock(ctx, repo, false, 0, func(string) {}, func(string, ...interface{}) {})
	if err != nil {
		return errBatch
	}
	defer unlock.Unlock()
	// Lock loss must also interrupt a worker blocked on a source pipe.
	stopLock := context.AfterFunc(lockedCtx, cancel)
	defer stopLock()
	if repo.LoadIndex(lockedCtx, nil) != nil {
		return errBatch
	}
	now := time.Now()
	filesystem, targets := newBatchFS(m, pipes, cancel, now)
	arch := archiver.New(repo, filesystem, archiver.Options{ReadConcurrency: uint(workers), SaveBlobConcurrency: 2, SaveTreeConcurrency: 2})
	arch.Error = func(_ string, _ error) error { cancel(); return errBatch }
	_, id, _, err := arch.Snapshot(lockedCtx, targets, archiver.SnapshotOptions{Time: now, BackupStart: now, Hostname: "weazlcloud", ProgramVersion: "weazlcloud-writer/restic-0.18.0"})
	if err != nil || id.IsNull() {
		return errBatch
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		SnapshotID string `json:"snapshot_id"`
	}{id.String()})
}

// Inherited descriptors are usually blocking. Register them with Go's poller
// so Close on cancellation actually interrupts a pending pipe read.
func inheritedFile(fd int) *os.File {
	if syscall.SetNonblock(fd, true) != nil {
		return nil
	}
	return os.NewFile(uintptr(fd), "input")
}
