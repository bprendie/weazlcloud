package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestQueueSkipsHistoricalHeadersAndAdmitsNewJobs(t *testing.T) {
	m, res, _ := fixture(t)
	create(t, m, res, []byte{}) // Empty component is a complete queued job.
	original := diskSession(t, res)
	// Historical corruption would fail a full header scan. A completed index
	// must neither open nor clean any of these terminal-history directories.
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("%032x", i)
		dir := keyFor(res, id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.enc"), []byte("unread historical ciphertext"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := setMarker(res, ".", ".indexed-v1", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		pending, err := m.Pending(res)
		if err != nil || len(pending) != 1 || pending[0].ID != original.ID {
			t.Fatalf("historical scan or missed queued job: %v %v", pending, err)
		}
	}
	id := fmt.Sprintf("%032x", 1000)
	if _, err := m.Create(res, owner, id, specFor(nil)); err != nil {
		t.Fatal(err)
	}
	pending, err := m.Pending(res)
	if err != nil || len(pending) != 2 {
		t.Fatalf("new queued job delayed by history: %v %v", pending, err)
	}
	if err = m.Sweep(res); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(keyFor(res, fmt.Sprintf("%032x", 1)), "session.enc")); err != nil {
		t.Fatal("sweep touched historical receipt")
	}
}

func TestLegacyRecoveryIsIncrementalAndQueueIsBounded(t *testing.T) {
	m, res, _ := fixture(t)
	create(t, m, res, nil)
	template := diskSession(t, res)
	// Simulate a pre-index deployment by writing encrypted headers without
	// markers. The first round must not enumerate/decrypt all 250 directories.
	for i := 0; i < 250; i++ {
		s := template
		s.ID = fmt.Sprintf("%032x", i)
		if err := writeSealed(res, filepath.Join(keyFor(res, s.ID), "session.enc"), s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Pending(res); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Root(res), ".indexed-v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy recovery completed in an unbounded first round")
	}
	seen := map[string]bool{}
	for i := 0; i < 15; i++ {
		pending, err := m.Pending(res)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) > 100 {
			t.Fatalf("pending exceeded batch bound: %d", len(pending))
		}
		for _, s := range pending {
			seen[s.ID] = true
		}
	}
	if len(seen) != 251 {
		t.Fatalf("bounded recovery/queue starved jobs: %d", len(seen))
	}
	if _, err := os.Stat(filepath.Join(Root(res), ".indexed-v1")); err != nil {
		t.Fatal("legacy migration did not finish")
	}
	// The completed migration is durable across manager restarts.
	restart := New(nil)
	pending, err := restart.Pending(res)
	if err != nil || len(pending) > 100 || len(pending) == 0 {
		t.Fatalf("restart queue: %v %v", pending, err)
	}
}

func TestQueueRepairsReceiptAndStaleTerminalMarkers(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("recover-ready-receipt")
	create(t, m, res, data)
	old := diskSession(t, res)
	appendPart(t, m, res, data, 0)
	// A crash after receipt persistence, before queue marker/session update.
	if err := writeSealed(res, filepath.Join(keyFor(res, sessionID), "session.enc"), old); err != nil {
		t.Fatal(err)
	}
	if err := setMarker(res, ".queue", sessionID, false); err != nil {
		t.Fatal(err)
	}
	if err := setMarker(res, ".", ".indexed-v1", true); err != nil {
		t.Fatal(err)
	}
	pending, err := m.Pending(res)
	if err != nil || len(pending) != 1 || pending[0].Status != "queued" {
		t.Fatalf("orphan receipt recovery: %v %v", pending, err)
	}
	if err = m.Process(context.Background(), res, sessionID, func(Session) error { return nil }, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	// A crash after terminal session persistence can retain its old marker.
	if err = setMarker(res, ".queue", sessionID, true); err != nil {
		t.Fatal(err)
	}
	pending, err = m.Pending(res)
	if err != nil || len(pending) != 0 {
		t.Fatalf("stale terminal marker: %v %v", pending, err)
	}
	if _, err = os.Stat(markerPath(res, ".queue", sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("terminal marker not removed")
	}
}
