package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// StreamInput names a normal, independently readable Restic file. Implementations
// must consume each reader through EOF, validate Size/Hash, and join all reads
// before returning. Inputs are never retained after PutStreams returns.
type StreamInput struct {
	Name   string
	Size   int64
	Hash   string
	Reader io.Reader
}

type photoStorageSingleKey struct{}

// WithPhotoStorageSingle isolates automatic retries from a failing batch peer.
func WithPhotoStorageSingle(ctx context.Context) context.Context {
	return context.WithValue(ctx, photoStorageSingleKey{}, true)
}

type photoStreamBackend interface {
	PutStreams(context.Context, []StreamInput) ([]catalog.Reference, error)
}

// ErrPhotoStreamsUnavailable permits stdin fallback only before any input read.
var ErrPhotoStreamsUnavailable = errors.New("photo stream batching unavailable")

// PhotoBatchRetryError requires reopening the durable encrypted component parts.
// A failed atomic snapshot is not evidence that every member's bytes are bad.
type PhotoBatchRetryError struct{ Err error }

func (e *PhotoBatchRetryError) Error() string {
	return fmt.Sprintf("retry photo storage batch: %v", e.Err)
}
func (e *PhotoBatchRetryError) Retryable() bool { return true }
func (e *PhotoBatchRetryError) Unwrap() error   { return e.Err }

const photoBatchBytes int64 = 64 << 20

type photoStreamRequest struct {
	ctx    context.Context
	input  StreamInput
	stream *photoStreamReader
	done   chan photoStreamResult
}
type photoStreamResult struct {
	ref     catalog.Reference
	release func()
	err     error
}

func (l *Library) putPhotoStream(ctx context.Context, input StreamInput, stream *photoStreamReader) (catalog.Reference, func(), error) {
	if single, _ := ctx.Value(photoStorageSingleKey{}).(bool); single {
		ref, err := l.backend.Put(ctx, input.Name, input.Reader)
		if err != nil {
			return ref, nil, &PhotoBatchRetryError{Err: err}
		}
		l.resticCommits.Add(1)
		release, err := l.backend.Hold(ref)
		return ref, release, err
	}
	w := l.componentWork()
	req := photoStreamRequest{ctx: ctx, input: input, stream: stream, done: make(chan photoStreamResult, 1)}
	// Admission is already capped at eight StorePhotoComponent calls.
	w.mu.Lock()
	w.queue <- req
	if !w.running {
		w.running = true
		go l.runPhotoStreams()
	}
	w.mu.Unlock()
	result := <-req.done // Cancellation cannot transfer ownership of a live reader.
	return result.ref, result.release, result.err
}

func (l *Library) runPhotoStreams() {
	w := l.componentWork()
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		first := <-w.queue
		w.mu.Unlock()
		requests := []photoStreamRequest{first}
		timer := time.NewTimer(batchWait)
	collect:
		for len(requests) < batchMax {
			select {
			case r := <-w.queue:
				requests = append(requests, r)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		// Split by declared bytes without allocating payload buffers. Large objects
		// retain constant-memory stdin streaming and never enter the native helper.
		for len(requests) > 0 {
			n, size := 1, requests[0].input.Size
			for n < len(requests) && size <= photoBatchBytes-requests[n].input.Size {
				size += requests[n].input.Size
				n++
			}
			l.storePhotoStreams(requests[:n])
			requests = requests[n:]
		}
	}
}

func (l *Library) storePhotoStreams(requests []photoStreamRequest) {
	var live []photoStreamRequest
	for _, r := range requests {
		if err := r.ctx.Err(); err != nil {
			r.done <- photoStreamResult{err: err}
		} else {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		return
	}
	// Request cancellation after dispatch must not kill unrelated batch members.
	ctx, release := l.previewContext(context.Background())
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	w := l.componentWork()
	w.mu.Lock()
	w.batchCancel = cancel
	if w.blocked == l.vault.Session() {
		cancel()
	}
	w.mu.Unlock()
	defer func() { cancel(); w.mu.Lock(); w.batchCancel = nil; w.mu.Unlock() }()
	inputs := make([]StreamInput, len(live))
	for i, r := range live {
		r.stream.ctx = ctx
		inputs[i] = r.input
	}
	var refs []catalog.Reference
	err := ErrPhotoStreamsUnavailable
	if b, ok := l.backend.(photoStreamBackend); ok && len(live) > 1 && live[0].input.Size <= photoBatchBytes {
		refs, err = b.PutStreams(ctx, inputs)
		if err == nil && len(refs) != len(live) {
			err = errors.New("photo batch reference count mismatch")
		}
		if err == nil {
			l.batchCommits.Add(1)
		}
	}
	if errors.Is(err, ErrPhotoStreamsUnavailable) {
		for _, r := range live {
			r.stream.ctx = r.ctx
			ref, e := l.backend.Put(r.ctx, r.input.Name, r.input.Reader)
			if e == nil {
				l.resticCommits.Add(1)
			} else if r.ctx.Err() == nil {
				e = &PhotoBatchRetryError{Err: e}
			}
			l.finishPhotoStream(r, ref, e)
		}
		return
	}
	if err != nil {
		err = &PhotoBatchRetryError{Err: err}
	}
	// Install every hold before releasing any result to catalog publication.
	l.mu.Lock()
	results := make([]photoStreamResult, len(live))
	for i := range live {
		results[i].err = err
		if err == nil {
			results[i].ref = refs[i]
			results[i].release, results[i].err = l.backend.Hold(refs[i])
		}
	}
	l.mu.Unlock()
	for i, r := range live {
		r.done <- results[i]
	}
}

func (l *Library) finishPhotoStream(r photoStreamRequest, ref catalog.Reference, err error) {
	var release func()
	if err == nil {
		release, err = l.backend.Hold(ref)
	}
	r.done <- photoStreamResult{ref: ref, release: release, err: err}
}
