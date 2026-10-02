package library

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/restic"
)

// Resident indexes must leave enough room for at least one maximum admitted
// source/decode pipeline, even when several unlocked owners use the node.
var previewIndexMemory = newPreviewMemoryBudget(max(0, previewPolicy.MemoryBytes-(2*thumbnailMaxInput+8*thumbnailMaxPixels+(192<<20))))

type previewReaderSession struct {
	reader *restic.Reader
	ready  chan struct{}
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	users  int
	idle   *time.Timer
}

// One charged index per unlocked owner. Allocation failure is an immediate CLI
// fallback, never a memory upgrade while holding another job's reservation.
func (l *Library) borrowPreviewReader(ctx context.Context) (*restic.Reader, func()) {
	noop := func() {}
	if _, ok := l.backend.(*resticBackend); !ok {
		return nil, noop
	}
	if os.Getenv("WEAZLCLOUD_PREVIEW_READER") == "off" || ctx.Err() != nil {
		return nil, noop
	}
	l.readerMu.Lock()
	if l.readerPaused > 0 {
		l.readerMu.Unlock()
		return nil, noop
	}
	s := l.previewReader
	if s != nil && s.ctx.Err() != nil {
		s = nil
	}
	if s == nil {
		path, err := exec.LookPath("weazl-restic-reader")
		budget := min(int64(2<<30), previewPolicy.MemoryBytes/4)
		workers := min(16, max(1, previewPolicy.SourceReaders))
		if err != nil || budget < 256<<20 {
			l.readerMu.Unlock()
			return nil, noop
		}
		resident := budget + int64(workers)*(32<<20)
		releaseIndex, ok := previewIndexMemory.tryAcquire(resident)
		if !ok {
			l.readerMu.Unlock()
			return nil, noop
		}
		releaseMemory, ok := previewMemory.tryAcquire(resident)
		if !ok {
			releaseIndex()
			l.readerMu.Unlock()
			return nil, noop
		}
		work, release := l.previewContext(context.Background())
		work, cancel := context.WithCancel(work)
		s = &previewReaderSession{ready: make(chan struct{}), done: make(chan struct{}), ctx: work, cancel: cancel}
		l.previewReader = s
		go func() {
			defer close(s.done)
			defer release()
			defer releaseMemory()
			defer releaseIndex()
			password, drive, err := l.vault.Secrets()
			clear(drive)
			if err == nil {
				s.reader, err = restic.StartReader(work, path, restic.Repo{Location: l.repo, Password: password}, workers, budget)
			}
			clear(password)
			if err != nil {
				cancel()
			}
			close(s.ready)
			<-work.Done()
			if s.reader != nil {
				s.reader.Close()
			}
			l.readerMu.Lock()
			if s.idle != nil {
				s.idle.Stop()
			}
			if l.previewReader == s {
				l.previewReader = nil
			}
			l.readerMu.Unlock()
		}()
	}
	s.users++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	l.readerMu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			l.readerMu.Lock()
			s.users--
			if s.users == 0 {
				s.idle = time.AfterFunc(time.Minute, func() {
					l.readerMu.Lock()
					if s.users == 0 {
						s.cancel()
					}
					l.readerMu.Unlock()
				})
			}
			l.readerMu.Unlock()
		})
	}
	select {
	case <-ctx.Done():
		release()
		return nil, noop
	case <-s.ready:
		if s.ctx.Err() != nil || s.reader == nil {
			release()
			return nil, noop
		}
		return s.reader, release
	}
}

// Explicit drain is used by maintenance and shutdown; vault-session cancellation
// independently closes a reader even when nobody is making API requests.
func (l *Library) stopPreviewReader() {
	l.readerMu.Lock()
	if s := l.previewReader; s != nil {
		s.cancel()
	}
	l.readerMu.Unlock()
}

func (l *Library) pausePreviewReader(ctx context.Context) (func(), error) {
	l.readerMu.Lock()
	l.readerPaused++
	s := l.previewReader
	if s != nil {
		s.cancel()
	}
	l.readerMu.Unlock()
	var once sync.Once
	resume := func() { once.Do(func() { l.readerMu.Lock(); l.readerPaused--; l.readerMu.Unlock() }) }
	if s != nil {
		select {
		case <-s.done:
		case <-ctx.Done():
			resume()
			return nil, ctx.Err()
		}
	}
	return resume, nil
}
