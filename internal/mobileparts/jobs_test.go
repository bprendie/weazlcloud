package mobileparts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRestartCountersAndRetryFinalization(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("recoverable-component")
	create(t, m, res, data)
	// Model a crash after the durable receipt but before session counters saved.
	stale := diskSession(t, res)
	appendPart(t, m, res, data, 0)
	path := filepath.Join(keyFor(res, sessionID), "session.enc")
	if err := writeSealed(res, path, stale); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseOwner(owner); err != nil {
		t.Fatal(err)
	}
	restart := New(q)
	t.Cleanup(func() { restart.ReleaseOwner(owner) })
	recovered, err := restart.Session(res, sessionID)
	if err != nil || recovered.Status != "queued" || recovered.Spec.Components[0].ReceivedBytes != int64(len(data)) || !recovered.UpdatedAt.Equal(stale.UpdatedAt) {
		t.Fatalf("restart: %+v %v", recovered, err)
	}
	pending, err := restart.Pending(res)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %v %v", pending, err)
	}
	publication := false
	calls := 0
	commit := func(s Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		calls++
		if !bytes.Equal(s.Spec.Payload, stale.Spec.Payload) || s.OwnerID != owner || s.Spec.DeviceID != device {
			t.Fatal("private identity metadata changed")
		}
		if !publication {
			r, err := open("original")
			if err != nil {
				return nil, err
			}
			got, err := io.ReadAll(r)
			r.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("commit input: %v", err)
			}
			publication = true
			return nil, os.ErrClosed // Publication succeeded; receipt update interrupted.
		}
		return json.RawMessage(`{"object":"durable"}`), nil
	}
	authorize := func(Session) error { return nil }
	if err = restart.Process(context.Background(), res, sessionID, authorize, commit); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("injected failure: %v", err)
	}
	failed, err := restart.Status(res, sessionID, device)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed status: %+v %v", failed, err)
	}
	restart.ReleaseOwner(owner)
	final := New(q)
	t.Cleanup(func() { final.ReleaseOwner(owner) })
	if _, err = final.Retry(res, sessionID, device); err != nil {
		t.Fatal(err)
	}
	if err = final.Process(context.Background(), res, sessionID, authorize, commit); err != nil {
		t.Fatal(err)
	}
	stored, err := final.Status(res, sessionID, device)
	if err != nil || stored.Status != "stored" || string(stored.Result) != `{"object":"durable"}` || calls != 2 {
		t.Fatalf("finalized: %+v calls=%d err=%v", stored, calls, err)
	}
	noPayload(t, res)
	if reserved(t, q) != 0 {
		t.Fatal("stored reservation leaked")
	}
}

func TestActiveRetryAndOwnerDrainCancellation(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("cancel-active-stream")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	started := make(chan context.Context)
	observe := make(chan struct{})
	finish := make(chan struct{})
	process := make(chan error, 1)
	go func() {
		process <- m.Process(context.Background(), res, sessionID, func(Session) error { return nil }, func(_ Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
			r, err := open("original")
			if err != nil {
				return nil, err
			}
			defer r.Close()
			started <- r.(*partsReader).ctx
			<-observe
			_, err = r.Read(make([]byte, 10))
			if !errors.Is(err, context.Canceled) {
				return nil, errors.New("owner drain did not cancel reader")
			}
			<-finish
			return nil, err
		})
	}()
	workerCtx := <-started
	if _, err := m.Retry(res, sessionID, device); !errors.Is(err, ErrConflict) {
		t.Fatalf("active retry: %v", err)
	}
	s := diskSession(t, res)
	if _, err := m.CancelCoordinated(res, sessionID, device, func(Session) (json.RawMessage, error) { t.Error("active cleanup callback executed"); return nil, nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("active coordinated cancel: %v", err)
	}
	if err := m.RebindPayload(res, sessionID, device, s.Spec.Payload, json.RawMessage(`{"grant":"fresh"}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("active rebind: %v", err)
	}
	if err := m.Cancel(res, sessionID, device); !errors.Is(err, ErrConflict) {
		t.Fatalf("active cancel: %v", err)
	}
	release := make(chan error, 1)
	go func() { release <- m.ReleaseOwner(owner) }()
	<-workerCtx.Done()
	m.mu.Lock()
	job := m.active[keyFor(res, sessionID)]
	m.mu.Unlock()
	select {
	case err := <-release:
		t.Fatalf("released before worker drain: %v", err)
	default:
	}
	if reserved(t, q) == 0 || job == nil {
		t.Fatal("released active worker quota")
	}
	close(observe)
	close(finish)
	if err := <-process; !errors.Is(err, context.Canceled) {
		t.Fatalf("worker cancellation: %v", err)
	}
	if err := <-release; err != nil {
		t.Fatal(err)
	}
	if reserved(t, q) != 0 {
		t.Fatal("owner drain leaked quota")
	}
	got, err := m.Status(res, sessionID, device)
	if err != nil || got.Status != "queued" {
		t.Fatalf("cancellation must allow retry: %+v %v", got, err)
	}
	if err = m.Cancel(res, sessionID, device); err != nil {
		t.Fatal(err)
	}
	noPayload(t, res)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.gates) != 0 || len(m.checked) != 0 || len(m.active) != 0 || len(m.reservations) != 0 || len(m.draining) != 0 {
		t.Fatal("idle manager retained session bookkeeping")
	}
}

func TestStoredReadRetriesFailedPayloadCleanup(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("already-stored-original")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	blocker := filepath.Join(keyFor(res, sessionID), "leftover.wza")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(blocker, "blocker")
	if err := os.WriteFile(child, []byte("ciphertext-placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	err := m.Process(context.Background(), res, sessionID, func(Session) error { return nil }, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		return json.RawMessage(`{"stored":true}`), nil
	})
	if err == nil {
		t.Fatal("expected failed payload cleanup")
	}
	if diskSession(t, res).Status != "stored" || reserved(t, q) != 0 {
		t.Fatal("cleanup failure lost stored state or leaked quota")
	}
	if err = os.Remove(child); err != nil {
		t.Fatal(err)
	}
	restart := New(q)
	got, err := restart.Status(res, sessionID, device)
	if err != nil || got.Status != "stored" {
		t.Fatalf("stored cleanup retry: %+v %v", got, err)
	}
	noPayload(t, res)
}
