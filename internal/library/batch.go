package library

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/restic"
)

const (
	batchWait = 35 * time.Millisecond
	batchMax  = 8
)

type batchRequest struct {
	stage   stagedUpload
	done    chan batchResult
	release func()
}

type batchResult struct {
	file catalog.File
	err  error
}

func (l *Library) commitStagedQueued(ctx context.Context, stage stagedUpload) (catalog.File, error) {
	request := batchRequest{stage: stage, done: make(chan batchResult, 1), release: l.trackStorage()}
	l.batchMu.Lock()
	l.batchPending = append(l.batchPending, request)
	if !l.batchRunning {
		l.batchRunning = true
		go l.runBatchCommits()
	}
	l.batchMu.Unlock()
	select {
	case l.batchWake <- struct{}{}:
	default:
	}
	select {
	case result := <-request.done:
		l.setStageActive(stage.ID, false)
		return result.file, result.err
	case <-ctx.Done():
		// The staged manifest remains durable. The coordinator may finish it
		// after the request has gone away, and a later Ensure can recover it.
		return catalog.File{}, ctx.Err()
	}
}

func (l *Library) runBatchCommits() {
	for {
		l.batchMu.Lock()
		if len(l.batchPending) == 0 {
			l.batchRunning = false
			l.batchMu.Unlock()
			return
		}
		requests := l.batchPending
		l.batchPending = nil
		l.batchMu.Unlock()
		timer := time.NewTimer(batchWait)
	collect:
		for len(requests) < batchMax {
			select {
			case <-l.batchWake:
				requests = append(requests, l.takeBatchRequests()...)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		l.processBatchRequests(requests)
	}
}

func (l *Library) takeBatchRequests() []batchRequest {
	l.batchMu.Lock()
	defer l.batchMu.Unlock()
	requests := l.batchPending
	l.batchPending = nil
	return requests
}

func (l *Library) processBatchRequests(requests []batchRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx := context.Background()
	if err := l.ensure(ctx); err != nil {
		for _, request := range requests {
			request.done <- batchResult{err: err}
		}
		l.finishBatchRequests(requests)
		return
	}
	counts := make(map[string]int)
	for _, request := range requests {
		counts[request.stage.Path]++
	}
	var batchable, singles []batchRequest
	for _, request := range requests {
		if counts[request.stage.Path] > 1 {
			singles = append(singles, request)
		} else {
			batchable = append(batchable, request)
		}
	}
	for _, request := range singles {
		file, err := l.commitStaged(ctx, request.stage)
		request.done <- batchResult{file: file, err: err}
	}
	if len(batchable) > 1 {
		err := l.commitStagedBatch(ctx, batchable)
		for _, request := range batchable {
			if err != nil {
				request.done <- batchResult{err: err}
				continue
			}
			file, ok := l.catalog.Get(request.stage.Path)
			if !ok {
				request.done <- batchResult{err: errors.New("batched upload was not published")}
				continue
			}
			request.done <- batchResult{file: file}
		}
	} else if len(batchable) == 1 {
		file, err := l.commitStaged(ctx, batchable[0].stage)
		batchable[0].done <- batchResult{file: file, err: err}
	}
	l.finishBatchRequests(requests)
}

func (l *Library) finishBatchRequests(requests []batchRequest) {
	for _, request := range requests {
		l.setStageActive(request.stage.ID, false)
		request.release()
	}
}

func (l *Library) commitStagedBatch(ctx context.Context, requests []batchRequest) error {
	if len(requests) < 2 {
		return nil
	}
	id, err := randomBatchID()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(l.repo), ".weazl-batch-"+id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, request := range requests {
		if err := linkStage(root, l.repo, request.stage); err != nil {
			_ = os.RemoveAll(root)
			return err
		}
	}
	pass, _, err := l.vault.Secrets()
	if err != nil {
		_ = os.RemoveAll(root)
		return err
	}
	snap, err := l.restic.PutBatch(ctx, restic.Repo{Location: l.repo, Password: pass}, root)
	if err != nil {
		_ = os.RemoveAll(root)
		return err
	}
	l.batchCommits.Add(1)
	for _, request := range requests {
		stage := request.stage
		stage.Snap = snap
		stage.BatchRoot = root
		stage.Object = filepath.Join(root, filepath.FromSlash(stage.Path))
		if err := l.writeStage(stage); err != nil {
			return err
		}
	}
	for _, request := range requests {
		stage := request.stage
		stage.Snap = snap
		stage.BatchRoot = root
		stage.Object = filepath.Join(root, filepath.FromSlash(stage.Path))
		f := catalog.File{Path: stage.Path, Size: stage.Size, Mtime: stage.Mtime, Hash: stage.Hash, Snap: snap, Object: stage.Object, Present: true}
		if err := l.catalog.Put(f); err != nil {
			return err
		}
		l.publishChange(Change{Kind: "put", Paths: []string{f.Path}})
		if err := l.removeStage(stage); err != nil {
			return err
		}
	}
	_ = os.RemoveAll(root)
	return nil
}

func randomBatchID() (string, error) {
	b, err := cryptox.Random(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func linkStage(root, repo string, stage stagedUpload) error {
	path, err := cleanPath(stage.Path)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	src := filepath.Join(repo, ".staging", stage.Data)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}
