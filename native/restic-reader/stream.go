package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

const streamChunk = 256 << 10

type responseStream struct {
	mu  sync.Mutex
	out io.Writer
}

// WRD2, kind, request ID, source size, payload length. Chunks never contain JSON
// or base64; the API admits the complete destination before requesting bytes.
func (s *responseStream) send(kind byte, id, size uint64, body []byte) error {
	if len(body) > streamChunk {
		return errors.New("oversized frame")
	}
	var header [25]byte
	copy(header[:4], "WRD2")
	header[4] = kind
	binary.BigEndian.PutUint64(header[5:13], id)
	binary.BigEndian.PutUint64(header[13:21], size)
	binary.BigEndian.PutUint32(header[21:25], uint32(len(body)))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.out.Write(header[:]); err != nil {
		return err
	}
	_, err := s.out.Write(body)
	return err
}

func serveBinary(parent context.Context, indexed *indexedRepository, workers int, input io.ReadCloser, output io.Writer) error {
	ctx, stop := context.WithCancel(parent)
	defer stop()
	defer input.Close()
	go func() { <-ctx.Done(); input.Close() }()
	stream := &responseStream{out: output}
	if err := stream.send(1, 0, 0, nil); err != nil {
		return err
	}
	var mu sync.Mutex
	active := map[uint64]context.CancelFunc{}
	var jobs sync.WaitGroup
	defer jobs.Wait()
	defer stop()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return errors.New("invalid request")
		}
		mu.Lock()
		if req.Cancel != 0 {
			if cancel := active[req.Cancel]; cancel != nil {
				cancel()
			}
			mu.Unlock()
			continue
		}
		if req.ID == 0 || req.Limit < 0 || req.Limit > 64<<20 || len(req.Snapshot) != 64 || len(req.Object) > 4096 || active[req.ID] != nil {
			mu.Unlock()
			return errors.New("invalid request")
		}
		if len(active) >= workers {
			mu.Unlock()
			if stream.send(5, req.ID, 0, nil) != nil {
				return io.ErrClosedPipe
			}
			continue
		}
		work, cancel := context.WithTimeout(ctx, 35*time.Second)
		active[req.ID] = cancel
		mu.Unlock()
		jobs.Add(1)
		go func(req request) {
			defer jobs.Done()
			defer cancel()
			err := indexed.read(work, func() error { return streamFile(work, indexed.repo, req, stream) })
			// Release the slot before the completion frame, so its consumer may
			// immediately issue a replacement request without a false busy result.
			mu.Lock()
			delete(active, req.ID)
			mu.Unlock()
			kind := byte(4)
			if err != nil {
				kind = 5
			}
			if stream.send(kind, req.ID, 0, nil) != nil {
				stop()
			}
		}(req)
	}
	return scanner.Err()
}

func streamFile(ctx context.Context, repo *repository.Repository, req request, stream *responseStream) error {
	node, err := findFile(ctx, repo, req.Snapshot, req.Object)
	if err != nil {
		return err
	}
	if err = stream.send(2, req.ID, node.Size, nil); err != nil {
		return err
	}
	remaining := min(uint64(req.Limit), node.Size)
	for _, id := range node.Content {
		if remaining == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		blob, err := repo.LoadBlob(ctx, restic.DataBlob, id, nil)
		if err != nil {
			return err
		}
		use := blob[:min(uint64(len(blob)), remaining)]
		for len(use) > 0 {
			part := use[:min(len(use), streamChunk)]
			if err = ctx.Err(); err == nil {
				err = stream.send(3, req.ID, 0, part)
			}
			if err != nil {
				clear(blob)
				return err
			}
			remaining -= uint64(len(part))
			use = use[len(part):]
		}
		clear(blob)
	}
	if remaining != 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}
