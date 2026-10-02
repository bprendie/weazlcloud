// WeazlCloud's bounded metadata reader uses Restic's authenticated read path.
// It never writes snapshots, packs, indexes, or catalog data.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/repository"
)

type request struct {
	ID       uint64 `json:"id"`
	Snapshot string `json:"snapshot"`
	Object   string `json:"object"`
	Limit    int    `json:"limit"`
}
type response struct {
	ID    uint64 `json:"id"`
	Body  []byte `json:"body,omitempty"`
	Size  uint64 `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
	Ready bool   `json:"ready,omitempty"`
}

func main() {
	repoPath := flag.String("repo", "", "local repository")
	workers := flag.Int("workers", 1, "bounded read concurrency")
	flag.Parse()
	if *workers < 1 || *workers > 16 || *repoPath == "" {
		os.Exit(2)
	}
	runtime.GOMAXPROCS(*workers)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, *repoPath, *workers); err != nil {
		// Do not expose repository paths, request contents, or credentials.
		fmt.Fprintln(os.Stderr, "metadata reader stopped:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, location string, workers int) error {
	passwordFD := os.NewFile(3, "repository-password")
	if passwordFD == nil {
		return errors.New("password pipe missing")
	}
	password, err := io.ReadAll(io.LimitReader(passwordFD, 257))
	passwordFD.Close()
	if err != nil || len(password) == 0 || len(password) > 256 {
		return errors.New("invalid password pipe")
	}
	defer clear(password)
	cfg := local.NewConfig()
	cfg.Path = location
	cfg.Connections = uint(workers)
	backend, err := local.Open(ctx, cfg)
	if err != nil {
		return errors.New("repository unavailable")
	}
	defer backend.Close()
	repo, err := repository.New(backend, repository.Options{})
	if err != nil {
		return errors.New("repository unavailable")
	}
	if err = repo.SearchKey(ctx, strings.TrimSpace(string(password)), 20, ""); err != nil {
		return errors.New("repository unlock failed")
	}
	clear(password)
	unlock, lockedCtx, err := repository.Lock(ctx, repo, false, 0, func(string) {}, func(string, ...interface{}) {})
	if err != nil {
		return errors.New("repository read lock unavailable")
	}
	defer unlock.Unlock()
	ctx = lockedCtx
	if err = repo.LoadIndex(ctx, nil); err != nil {
		return errors.New("repository index unavailable")
	}
	enc := json.NewEncoder(os.Stdout)
	if err = enc.Encode(response{Ready: true}); err != nil {
		return err
	}
	return serve(ctx, repo, workers, enc)
}

func serve(ctx context.Context, repo *repository.Repository, workers int, enc *json.Encoder) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); os.Stdin.Close() }()
	var writes sync.Mutex
	var jobs sync.WaitGroup
	slots := make(chan struct{}, workers)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == 0 || req.Limit < 0 || req.Limit > 4<<20 || len(req.Object) > 4096 {
			cancel()
			jobs.Wait()
			return errors.New("invalid read request")
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			jobs.Wait()
			return ctx.Err()
		}
		jobs.Add(1)
		go func(req request) {
			defer jobs.Done()
			defer func() { <-slots }()
			readCtx, release := context.WithTimeout(ctx, 30*time.Second)
			result := readFile(readCtx, repo, req)
			release()
			writes.Lock()
			if enc.Encode(result) != nil {
				cancel()
			}
			writes.Unlock()
			clear(result.Body)
		}(req)
	}
	cancel()
	jobs.Wait()
	return nil
}
