package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingRunsValidNeighborsAndPreservesBadReceipts(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		name := "legacy-recovery"
		if indexed {
			name = "live-recovery"
		}
		t.Run(name, func(t *testing.T) {
			m, res, _ := fixture(t)
			corruptID, futureID := strings.Repeat("1", 32), strings.Repeat("2", 32)
			for _, id := range []string{corruptID, futureID} {
				if _, err := m.Create(res, owner, id, specFor(nil)); err != nil {
					t.Fatal(err)
				}
			}
			create(t, m, res, nil)
			if err := os.WriteFile(filepath.Join(keyFor(res, corruptID), "session.enc"), []byte("corrupt sealed receipt"), 0600); err != nil {
				t.Fatal(err)
			}
			var future Session
			futurePath := filepath.Join(keyFor(res, futureID), "session.enc")
			if err := readSealed(res, futurePath, &future); err != nil {
				t.Fatal(err)
			}
			future.Version = 2
			if err := writeSealed(res, futurePath, future); err != nil {
				t.Fatal(err)
			}
			if indexed {
				if err := setMarker(res, ".", ".indexed-v1", true); err != nil {
					t.Fatal(err)
				}
			} else {
				// Recovery must discover a valid header after either bad legacy header.
				for _, id := range []string{corruptID, futureID, sessionID} {
					if err := dropSessionMarkers(res, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			corruptBefore := snapshotSession(t, keyFor(res, corruptID))
			futureBefore := snapshotSession(t, keyFor(res, futureID))
			queueBefore := snapshotSession(t, filepath.Join(Root(res), ".queue"))
			liveBefore := snapshotSession(t, filepath.Join(Root(res), ".live"))
			pending, warning := m.Pending(res)
			if warning == nil || !errors.Is(warning, ErrCorrupt) || len(pending) != 1 || pending[0].ID != sessionID {
				t.Fatalf("partial pending: jobs=%v warning=%v", pending, warning)
			}
			// Both distinct failures survive aggregation; one bad candidate cannot
			// truncate either incremental recovery or the queue candidate loop.
			if warningLeaves(warning) < 2 {
				t.Fatalf("missing joined warnings: %v", warning)
			}
			commitCalled := false
			if err := m.Process(context.Background(), res, pending[0].ID, func(Session) error { return nil }, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
				commitCalled = true
				return json.RawMessage(`{"stored":true}`), nil
			}); err != nil {
				t.Fatal(err)
			}
			if !commitCalled || diskSession(t, res).Status != "stored" {
				t.Fatal("valid neighbor did not run")
			}
			unchangedSessionFiles(t, corruptBefore, snapshotSession(t, keyFor(res, corruptID)))
			unchangedSessionFiles(t, futureBefore, snapshotSession(t, keyFor(res, futureID)))
			for _, index := range []string{".queue", ".live"} {
				before := queueBefore
				if index == ".live" {
					before = liveBefore
				}
				after := snapshotSession(t, filepath.Join(Root(res), index))
				for _, id := range []string{corruptID, futureID} {
					want, existed := before[id]
					got, exists := after[id]
					if existed != exists || existed && (want.mode != got.mode || !want.modified.Equal(got.modified) || string(want.body) != string(got.body)) {
						t.Fatalf("bad receipt marker changed: %s/%s", index, id)
					}
				}
			}
			// Sweep also continues its bounded batch, preserving both bad receipts.
			now := future.UpdatedAt.Add(Lifetime + time.Hour)
			m.now = func() time.Time { return now }
			if indexed {
				freshID := strings.Repeat("3", 32)
				s := future
				s.Version, s.ID, s.Status = 1, freshID, "uploading"
				if err := writeSealed(res, filepath.Join(keyFor(res, freshID), "session.enc"), s); err != nil {
					t.Fatal(err)
				}
				if err := syncSessionMarkers(res, s); err != nil {
					t.Fatal(err)
				}
				if err := m.Sweep(res); err == nil {
					t.Fatal("sweep lost warning")
				}
				if _, err := os.Stat(keyFor(res, freshID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("bad neighbor blocked valid expiry: %v", err)
				}
				unchangedSessionFiles(t, corruptBefore, snapshotSession(t, keyFor(res, corruptID)))
				unchangedSessionFiles(t, futureBefore, snapshotSession(t, keyFor(res, futureID)))
			}
		})
	}
}

func warningLeaves(err error) int {
	if err == nil {
		return 0
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		count := 0
		for _, child := range joined.Unwrap() {
			count += warningLeaves(child)
		}
		return count
	}
	return 1
}
