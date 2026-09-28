package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func putPreviewFixture(t *testing.T, l *Library, name string) catalog.File {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put(context.Background(), name, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	f, err := l.Metadata(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type blockingPreviewBackend struct {
	Backend
	started, proceed chan struct{}
	once             sync.Once
}

func (b *blockingPreviewBackend) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.proceed:
	}
	return b.Backend.Read(ctx, ref, w)
}

func TestPreviewCannotReturnDeletedOrReplacedBytes(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{true: "replacement", false: "deletion"}[replace], func(t *testing.T) {
			l := newPhotoIndexTestLibrary(t)
			f := putPreviewFixture(t, l, "Photos/a.png")
			b := &blockingPreviewBackend{Backend: l.backend, started: make(chan struct{}), proceed: make(chan struct{})}
			l.backend = b
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				body, _, err := l.Thumbnail(ctx, f.Path, 320)
				if len(body) > 0 {
					result <- errors.New("returned old bytes")
				} else {
					result <- err
				}
			}()
			select {
			case <-b.started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if replace {
				if _, err := l.Put(ctx, f.Path, []byte("replacement")); err != nil {
					t.Fatal(err)
				}
			} else if err := l.Delete(f.Path); err != nil {
				t.Fatal(err)
			}
			close(b.proceed)
			if err := <-result; err == nil || err.Error() == "returned old bytes" {
				t.Fatalf("stale preview: %v", err)
			}
		})
	}
}

func TestPreviewCacheHitChecksFileAndVaultSession(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	f := putPreviewFixture(t, l, "Photos/a.png")
	if _, _, err := l.Thumbnail(context.Background(), f.Path, 320); err != nil {
		t.Fatal(err)
	}
	ctx, release := l.previewContext(context.Background())
	defer release()
	l.vault.Lock()
	if err := l.vault.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	if err := l.validatePreview(ctx, f); err == nil {
		t.Fatal("old vault session still authorized")
	}
	if err := l.Delete(f.Path); err != nil {
		t.Fatal(err)
	}
	if body, _, err := l.thumbnailFor(context.Background(), f, 320, false); err == nil || len(body) > 0 {
		t.Fatal("deleted file served from cache")
	}
}

func TestBoundedPreviewWriterCannotBeBypassedByCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &boundedPreviewWriter{limit: 4, cancel: cancel}
	_, err := io.Copy(w, bytes.NewReader([]byte("longer than declared")))
	if err == nil || w.Len() != 4 || ctx.Err() == nil {
		t.Fatalf("unbounded read: len=%d err=%v", w.Len(), err)
	}
}

func TestCoalescedPreviewFirstWaiterCancellationDoesNotCancelOthers(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	started, proceed := make(chan struct{}), make(chan struct{})
	render := func(ctx context.Context) ([]byte, string, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-proceed:
			return []byte("ok"), "image/png", nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, _, err := l.coalescedPreview(ctx, "key", render); first <- err }()
	<-started
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	go func() { _, _, err := l.coalescedPreview(deadline, "key", render); second <- err }()
	for {
		l.thumbMu.Lock()
		n := l.thumbJobs["key"].waiters
		l.thumbMu.Unlock()
		if n == 2 {
			break
		}
		if deadline.Err() != nil {
			t.Fatal(deadline.Err())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
