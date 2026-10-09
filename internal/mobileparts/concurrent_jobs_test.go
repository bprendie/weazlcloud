package mobileparts

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentProcessPublishesOnce(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("one publication for simultaneous finalizers")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	commit := func(_ Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		r, err := open("original")
		if err != nil {
			return nil, err
		}
		defer r.Close()
		if _, err = io.Copy(io.Discard, r); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"stored":true}`), nil
	}
	auth := func(Session) error { return nil }
	first := make(chan error, 1)
	go func() { first <- m.Process(context.Background(), res, sessionID, auth, commit) }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Process(context.Background(), res, sessionID, auth, commit); err != nil {
				t.Errorf("duplicate process: %v", err)
			}
		}()
	}
	wg.Wait()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	s, err := m.Status(res, sessionID, device)
	if err != nil || s.Status != "stored" || calls.Load() != 1 {
		t.Fatalf("status=%s publications=%d err=%v", s.Status, calls.Load(), err)
	}
	noPayload(t, res)
}
