package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type componentBackend struct {
	Backend
	mu               sync.Mutex
	objects          map[string][]byte
	batches, singles int
	counts           []int
	before           func(context.Context, []StreamInput) error
}

func (b *componentBackend) Ensure(context.Context) error           { return nil }
func (b *componentBackend) Drain(context.Context) error            { return nil }
func (b *componentBackend) Hold(catalog.Reference) (func(), error) { return func() {}, nil }
func (b *componentBackend) Put(ctx context.Context, name string, r io.Reader) (catalog.Reference, error) {
	b.mu.Lock()
	b.singles++
	b.mu.Unlock()
	refs, err := b.store([]StreamInput{{Name: name, Reader: r}})
	if err != nil {
		return catalog.Reference{}, err
	}
	return refs[0], nil
}
func (b *componentBackend) PutStreams(ctx context.Context, in []StreamInput) ([]catalog.Reference, error) {
	b.mu.Lock()
	b.batches++
	b.counts = append(b.counts, len(in))
	b.mu.Unlock()
	if b.before != nil {
		if err := b.before(ctx, in); err != nil {
			return nil, err
		}
	}
	return b.store(in)
}
func (b *componentBackend) store(in []StreamInput) ([]catalog.Reference, error) {
	refs := make([]catalog.Reference, len(in))
	for i, input := range in {
		body, err := io.ReadAll(input.Reader)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.objects[input.Name] = body
		b.mu.Unlock()
		refs[i] = resticReference("batch-snapshot", input.Name, "")
	}
	return refs, nil
}
func componentFixture(t *testing.T) (*Library, *componentBackend) {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "key"))
	if err := v.Forge([]byte("component"), []byte("component")); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)
	b := &componentBackend{objects: make(map[string][]byte)}
	l.backend = b
	l.photoAutoDisabled = true
	t.Cleanup(func() { _ = l.Drain(context.Background()); v.Lock() })
	return l, b
}
func componentHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func storeComponent(l *Library, ctx context.Context, name string, body []byte) (catalog.File, error) {
	return l.StorePhotoComponent(ctx, ".weazl-mobile-pending/"+name, bytes.NewReader(body), int64(len(body)), componentHash(body))
}
func awaitComponent(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("component did not finish")
		return nil
	}
}
func TestPhotoStreamsBatchAndConcurrentRetry(t *testing.T) {
	l, b := componentFixture(t)
	start := make(chan struct{})
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			<-start
			body := []byte(fmt.Sprintf("original-%d", i))
			f, e := storeComponent(l, context.Background(), fmt.Sprint(i), body)
			if e == nil {
				b.mu.Lock()
				got := b.objects[f.Reference.Object]
				b.mu.Unlock()
				if !bytes.Equal(got, body) {
					e = errors.New("wrong original")
				}
			}
			errs <- e
		}(i)
	}
	close(start)
	for i := 0; i < 8; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	b.mu.Lock()
	if b.batches != 1 || b.singles != 0 {
		t.Errorf("batches=%d singles=%d", b.batches, b.singles)
	}
	b.mu.Unlock()
	for i := 0; i < 8; i++ {
		_, err := l.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/0", failPhotoComponentReader{t}, 10, componentHash([]byte("original-0")))
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestPhotoStreamsCancellationDoesNotCancelPeer(t *testing.T) {
	l, b := componentFixture(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	b.before = func(ctx context.Context, _ []StreamInput) error {
		close(entered)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	one, two := make(chan error, 1), make(chan error, 1)
	go func() { _, e := storeComponent(l, ctx, "one", []byte("one")); one <- e }()
	go func() { _, e := storeComponent(l, context.Background(), "two", []byte("two")); two <- e }()
	<-entered
	cancel()
	select {
	case <-one:
		t.Fatal("returned while helper still owns reader")
	default:
	}
	// Catalog operations continue while helper is blocked.
	summary := make(chan error, 1)
	go func() { _, e := l.Summary(context.Background()); summary <- e }()
	if err := awaitComponent(t, summary); err != nil {
		t.Fatal(err)
	}
	close(unblock)
	if err := awaitComponent(t, one); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := awaitComponent(t, two); err != nil {
		t.Fatal(err)
	}
	protected, err := l.photoComponentSnapshots()
	if err != nil || !protected["batch-snapshot"] {
		t.Fatalf("intent snapshot not protected: %v %v", protected, err)
	}
	// Simulate a process restart; the verified encrypted intent avoids reading bytes.
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = b
	restarted.photoAutoDisabled = true
	defer restarted.Drain(context.Background())
	f, err := restarted.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/one", failPhotoComponentReader{t}, 3, componentHash([]byte("one")))
	if err != nil || f.Reference.Snapshot != "batch-snapshot" {
		t.Fatalf("restart: %+v %v", f, err)
	}
}

func TestPhotoStreamsBadMemberIsRetryableForPeer(t *testing.T) {
	l, b := componentFixture(t)
	_ = b
	bad, good := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := l.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/bad", bytes.NewReader([]byte("bad")), 3, componentHash([]byte("yes")))
		bad <- e
	}()
	go func() { _, e := storeComponent(l, context.Background(), "good", []byte("good")); good <- e }()
	if err := awaitComponent(t, bad); !errors.Is(err, ErrPhotoComponentChecksum) {
		t.Fatalf("bad: %v", err)
	}
	var retry *PhotoBatchRetryError
	if err := awaitComponent(t, good); !errors.As(err, &retry) || !retry.Retryable() {
		t.Fatalf("peer: %v", err)
	}
	if _, err := storeComponent(l, context.Background(), "good", []byte("good")); err != nil {
		t.Fatal(err)
	}
}

func TestPhotoStreamsDrainAndVaultLock(t *testing.T) {
	for _, lock := range []bool{false, true} {
		t.Run(fmt.Sprint(lock), func(t *testing.T) {
			l, b := componentFixture(t)
			entered := make(chan struct{})
			b.before = func(ctx context.Context, _ []StreamInput) error { close(entered); <-ctx.Done(); return ctx.Err() }
			errs := make(chan error, 2)
			for _, name := range []string{"one", "two"} {
				go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
			}
			<-entered
			if lock {
				l.PrepareVaultLock()
				l.vault.Lock()
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := l.Drain(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := awaitComponent(t, errs); !errors.Is(err, context.Canceled) {
					t.Fatalf("drain: %v", err)
				}
			}
			if _, err := storeComponent(l, context.Background(), "late", []byte("late")); err == nil {
				t.Fatal("accepted work after stop")
			}
		})
	}
}

func TestPhotoStreamsCancellationCleanupWaitsForStorage(t *testing.T) {
	l, b := componentFixture(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	b.before = func(context.Context, []StreamInput) error { close(entered); <-unblock; return nil }
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) { _, e := storeComponent(l, context.Background(), name, []byte(name)); errs <- e }(name)
	}
	<-entered
	canceled := make(chan error, 1)
	tombstoned := make(chan struct{})
	go func() {
		_, e := l.CancelPhotoIngest(context.Background(), catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "asset", SourceRevision: "1", Files: []catalog.PhotoIngestFile{{ID: "original", From: ".weazl-mobile-pending/one", Size: 3, Hash: componentHash([]byte("one"))}}}, func() error { close(tombstoned); return nil })
		canceled <- e
	}()
	select {
	case <-tombstoned:
		t.Fatal("tombstone raced active storage")
	case <-time.After(20 * time.Millisecond):
	}
	close(unblock)
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitComponent(t, canceled); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.catalog.Get(".weazl-mobile-pending/one"); ok {
		t.Fatal("canceled row survived")
	}
	assertComponentIntentsSettled(t, l)
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = b
	restarted.photoAutoDisabled = true
	defer restarted.Drain(context.Background())
	_, err := restarted.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/one", failPhotoComponentReader{t}, 3, componentHash([]byte("one")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled component recreated after restart: %v", err)
	}
	assertComponentIntentsSettled(t, restarted)
}
