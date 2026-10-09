package mobileparts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestForeignCancelDoesNotInterruptReceiver(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("private receiver")
	create(t, m, res, data)
	r := &heldReceive{entered: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(data)}
	done := make(chan error, 1)
	go func() {
		_, e := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), r)
		done <- e
	}()
	<-r.entered
	if e := m.Cancel(res, sessionID, strings.Repeat("f", 32)); !errors.Is(e, ErrNotFound) {
		t.Error(e)
	}
	close(r.release)
	if e := <-done; e != nil {
		t.Fatal("foreign caller interrupted upload", e)
	}
}
func TestOwnerReleaseClosesAndJoinsReceiver(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("owner release")
	create(t, m, res, data)
	pipe, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, e := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), pipe)
		done <- e
	}()
	until := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		active := len(m.receivers) > 0
		m.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(until) {
			t.Fatal("no receiver")
		}
		time.Sleep(time.Millisecond)
	}
	released := make(chan error, 1)
	go func() { released <- m.ReleaseOwner(owner) }()
	select {
	case e := <-released:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("owner release stuck")
	}
	if e := <-done; e == nil {
		t.Fatal("released upload succeeded")
	}
	if reserved(t, q) != 0 {
		t.Fatal("reservation not released")
	}
	noPayload(t, res)
}
func TestFinalizerCannotDeleteActiveDuplicateTemporaryFile(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("duplicate race")
	create(t, m, res, data)
	r := &heldReceive{entered: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(data)}
	done := make(chan error, 1)
	go func() {
		_, e := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), r)
		done <- e
	}()
	<-r.entered
	appendPart(t, m, res, data, 0)
	called := false
	e := m.Process(context.Background(), res, sessionID, func(Session) error { return nil }, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		called = true
		return nil, nil
	})
	close(r.release)
	if appendErr := <-done; appendErr != nil {
		t.Fatal(appendErr)
	}
	if e != nil || called {
		t.Fatal("finalizer admitted during receive", e)
	}
	v, e := m.Status(res, sessionID, device)
	if e != nil || v.Status != "queued" || v.Components[0].ReceivedParts != 1 {
		t.Fatal(v, e)
	}
}

func TestCancelledAdmissionDoesNotWaitForSessionGate(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("gate wait")
	create(t, m, res, data)
	unlock := m.lock(res, sessionID)
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, e := m.Append(ctx, res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(data))
		result <- e
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case e := <-result:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelled admission retained gate wait")
	}

}
