package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatedCancelErrorAndStoredProof(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("published-original-must-survive")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	before := snapshotSession(t, keyFor(res, sessionID))
	_, err := m.CancelCoordinated(res, sessionID, device, func(Session) (json.RawMessage, error) { return nil, os.ErrClosed })
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("cleanup failure: %v", err)
	}
	unchangedSessionFiles(t, before, snapshotSession(t, keyFor(res, sessionID)))
	published := filepath.Join(t.TempDir(), "published.enc")
	if err = os.WriteFile(published, []byte("durable-encrypted-published-original"), 0600); err != nil {
		t.Fatal(err)
	}
	proof := json.RawMessage(`{"object":"already-published"}`)
	got, err := m.CancelCoordinated(res, sessionID, device, func(s Session) (json.RawMessage, error) {
		if s.Status != "queued" {
			t.Fatal("coordinator did not see engine state")
		}
		return proof, nil
	})
	if err != nil || got.Status != "stored" || string(got.Result) != string(proof) {
		t.Fatalf("stored proof: %+v %v", got, err)
	}
	if _, err = os.Stat(published); err != nil {
		t.Fatal("coordinated cancel removed published file")
	}
	noPayload(t, res)
	if reserved(t, q) != 0 {
		t.Fatal("stored proof leaked workspace")
	}
	if _, err = m.Create(res, owner, sessionID, specFor(data)); err != nil {
		t.Fatalf("stored replay: %v", err)
	}
}

func TestCoordinatedCancelRecoveryAndWorkerSerialization(t *testing.T) {
	m, res, q := fixture(t)
	create(t, m, res, nil)
	entered := make(chan struct{})
	resume := make(chan struct{})
	result := make(chan error, 1)
	durableCancelled := false
	go func() {
		_, err := m.CancelCoordinated(res, sessionID, device, func(Session) (json.RawMessage, error) {
			durableCancelled = true
			close(entered)
			<-resume
			return nil, nil
		})
		result <- err
	}()
	<-entered
	if !durableCancelled {
		t.Fatal("coordinator tombstone must precede engine transition")
	}
	process := make(chan error, 1)
	commitCalled := false
	go func() {
		process <- m.Process(context.Background(), res, sessionID, func(Session) error { return nil }, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
			commitCalled = true
			return json.RawMessage(`{}`), nil
		})
	}()
	close(resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-process; !errors.Is(err, ErrConflict) {
		t.Fatalf("worker published after cancellation: %v", err)
	}
	if commitCalled {
		t.Fatal("cancelled session reached commit")
	}
	// A repeated DELETE retries coordinator cleanup against the durable tombstone.
	calls := 0
	got, err := m.CancelCoordinated(res, sessionID, device, func(s Session) (json.RawMessage, error) {
		calls++
		if s.Status != "cancelled" {
			t.Fatal("lost cancellation tombstone")
		}
		return nil, nil
	})
	if err != nil || got.Status != "cancelled" || calls != 1 {
		t.Fatalf("cleanup retry: %+v %v", got, err)
	}
	now := got.UpdatedAt.Add(100 * Lifetime)
	m.now = func() time.Time { return now }
	if err = m.Sweep(res); err != nil {
		t.Fatal(err)
	}
	stored := diskSession(t, res)
	if stored.Status != "cancelled" || reserved(t, q) != 0 {
		t.Fatal("cancelled tombstone lost or leaked quota")
	}
}
