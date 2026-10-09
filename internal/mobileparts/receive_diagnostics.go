package mobileparts

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/quota"
	"io"
	"log"
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Timings are wall time. Read time includes transport and authorization; commit
// includes its publication guard. These nested durations must not be summed.
type receiveTrace struct {
	start                                     time.Time
	expected, bytes                           int64
	admission, read, sync, commitWait, commit time.Duration
}
type receiveReader struct {
	ctx   context.Context
	src   io.Reader
	trace *receiveTrace
}

func (r *receiveReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	start := time.Now()
	n, e := r.src.Read(p)
	r.trace.read += time.Since(start)
	r.trace.bytes += int64(n)
	return n, e
}

var receiveActive atomic.Int64
var receiveCount, receiveErrors, receiveBytes atomic.Uint64

func (t receiveTrace) finish(err error) {
	n := receiveCount.Add(1)
	if err != nil {
		receiveErrors.Add(1)
	}
	if t.bytes > 0 {
		receiveBytes.Add(uint64(t.bytes))
	}
	if err == nil && n%32 != 0 && time.Since(t.start) < 10*time.Second {
		return
	}
	log.Printf("mobile receive expected=%d received=%d admission_ms=%d read_ms=%d sync_ms=%d commit_wait_ms=%d commit_ms=%d total_ms=%d outcome=%s requests=%d errors=%d bytes=%d active=%d", t.expected, t.bytes, t.admission.Milliseconds(), t.read.Milliseconds(), t.sync.Milliseconds(), t.commitWait.Milliseconds(), t.commit.Milliseconds(), time.Since(t.start).Milliseconds(), ReceiveFailureClass(err), n, receiveErrors.Load(), receiveBytes.Load(), receiveActive.Load())
}
func ReceiveFailureClass(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.As(err, &ne) && ne.Timeout():
		return "read_timeout"
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		return "unexpected_eof"
	case errors.Is(err, users.ErrNoSession) || errors.Is(err, users.ErrInsufficientScope):
		return "authorization"
	case errors.Is(err, vault.ErrLocked):
		return "vault_locked"
	case errors.Is(err, ErrChecksum):
		return "checksum"
	case errors.Is(err, quota.ErrExceeded) || errors.Is(err, syscall.ENOSPC):
		return "capacity"
	case errors.As(err, &ne):
		return "network"
	case errors.Is(err, ErrBusy):
		return "admission_busy"
	case errors.Is(err, ErrInvalid):
		return "invalid_or_length"
	case errors.Is(err, ErrConflict):
		return "conflict"
	default:
		return "storage"
	}
}
