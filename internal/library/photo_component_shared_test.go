package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

type componentBarrierReader struct {
	reader  io.Reader
	entered chan<- struct{}
	resume  <-chan struct{}
	once    bool
}

func (r *componentBarrierReader) Read(p []byte) (int, error) {
	if !r.once {
		r.once = true
		r.entered <- struct{}{}
		<-r.resume
	}
	return r.reader.Read(p)
}

func TestPhotoComponentSharedParallelAndRestart(t *testing.T) {
	l, _ := componentFixture(t)
	var fail atomic.Bool
	fail.Store(true)
	injected := errors.New("crash after durable prepare")
	store, err := sharedstore.Open(filepath.Join(t.TempDir(), "shared"), sharedstore.Options{FailureHook: func(point string) error {
		if point == "prepared" && fail.Load() {
			return injected
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	l.ConfigureShared("owner", store, true)
	entered, resume := make(chan struct{}, 2), make(chan struct{})
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			body := []byte(name)
			_, e := l.StorePhotoComponent(context.Background(), ".weazl-mobile-pending/"+name, &componentBarrierReader{reader: bytes.NewReader(body), entered: entered, resume: resume}, 3, componentHash(body))
			errs <- e
		}(name)
	}
	// Both readers must enter preparation concurrently without holding l.mu.
	both := make(chan error, 1)
	go func() { <-entered; <-entered; both <- nil }()
	if err := awaitComponent(t, both); err != nil {
		t.Fatal(err)
	}
	summary := make(chan error, 1)
	go func() { _, e := l.Summary(context.Background()); summary <- e }()
	if err := awaitComponent(t, summary); err != nil {
		t.Fatal(err)
	}
	close(resume)
	for i := 0; i < 2; i++ {
		if err := awaitComponent(t, errs); !errors.Is(err, injected) {
			t.Fatalf("prepare: %v", err)
		}
	}
	intentPath, err := l.photoComponentIntentPath(".weazl-mobile-pending/one", 3, componentHash([]byte("one")))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := l.loadPhotoComponentIntent(intentPath, ".weazl-mobile-pending/one", 3, componentHash([]byte("one")))
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(false)
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = l.backend
	restarted.photoAutoDisabled = true
	restarted.ConfigureShared("owner", store, true)
	defer restarted.Drain(context.Background())
	for _, name := range []string{"one", "two"} {
		f, err := storeComponent(restarted, context.Background(), name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
		if f.Reference.Backend != catalog.SharedBackend {
			t.Fatal("changed backend")
		}
		if name == "one" && (f.EntryID != intent.File.EntryID || f.Reference.Operation != intent.Operation) {
			t.Fatal("retry changed prepared identity")
		}
		var restored bytes.Buffer
		if err := restarted.readReference(context.Background(), *f.Reference, &restored); err != nil || restored.String() != name {
			t.Fatalf("read integrity: %q %v", restored.String(), err)
		}
		if _, err := restarted.StorePhotoComponent(context.Background(), f.Path, failPhotoComponentReader{t}, 3, componentHash([]byte(name))); err != nil {
			t.Fatal(err)
		}
	}
}
