package mobileparts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/quota"
)

func TestSweepPreservesExpiredCancelledTombstone(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("cancelled-receipt-original")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	if err := m.Cancel(res, sessionID, device); err != nil {
		t.Fatal(err)
	}
	before := diskSession(t, res)
	files := snapshotSession(t, keyFor(res, sessionID))
	now := before.UpdatedAt.Add(100 * Lifetime)
	m.now = func() time.Time { return now }
	if err := m.Sweep(res); err != nil {
		t.Fatal(err)
	}
	unchangedSessionFiles(t, files, snapshotSession(t, keyFor(res, sessionID)))
	current, err := m.Session(res, sessionID)
	if err != nil || current.Status != "cancelled" || !current.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("cancelled tombstone lost: %+v %v", current, err)
	}
	replay, err := m.Create(res, owner, sessionID, specFor(data))
	if err != nil || replay.Status != "cancelled" {
		t.Fatalf("cancelled replay recreated session: %+v %v", replay, err)
	}
	if err = m.RebindPayload(res, sessionID, device, before.Spec.Payload, json.RawMessage(`{"grant":"fresh"}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled reauth: %v", err)
	}
	noPayload(t, res)
	if reserved(t, q) != 0 {
		t.Fatal("cancelled tombstone reserved space")
	}
}

func TestExpiredGrantRebindRestartsStaging(t *testing.T) {
	for _, status := range []string{"uploading", "queued", "verifying", "failed"} {
		t.Run(status, func(t *testing.T) {
			m, res, q := fixture(t)
			data := []byte("expired-encrypted-original")
			create(t, m, res, data)
			appendPart(t, m, res, data, 0)
			before := diskSession(t, res)
			before.Status = status
			before.ErrorCode = "previous-failure"
			if err := writeSealed(res, filepath.Join(keyFor(res, sessionID), "session.enc"), before); err != nil {
				t.Fatal(err)
			}
			now := before.UpdatedAt.Add(Lifetime + time.Hour)
			m.now = func() time.Time { return now }
			expired, err := m.Session(res, sessionID)
			if !errors.Is(err, ErrExpired) || !bytes.Equal(expired.Spec.Payload, before.Spec.Payload) {
				t.Fatalf("expired private read: %+v %v", expired, err)
			}
			replacement := json.RawMessage(`{"name":"private-mobile-file","authorization_version":2}`)
			files := snapshotSession(t, keyFor(res, sessionID))
			if err = m.RebindPayload(res, sessionID, device, json.RawMessage(`{"stale":"grant"}`), replacement); !errors.Is(err, ErrConflict) {
				t.Fatalf("expired CAS accepted: %v", err)
			}
			unchangedSessionFiles(t, files, snapshotSession(t, keyFor(res, sessionID)))
			if err = m.RebindPayload(res, sessionID, device, expired.Spec.Payload, replacement); err != nil {
				t.Fatal(err)
			}
			fresh, err := m.Session(res, sessionID)
			if err != nil || fresh.Status != "uploading" || fresh.ErrorCode != "" || fresh.Result != nil || fresh.Key == before.Key || !fresh.UpdatedAt.Equal(now) || !bytes.Equal(fresh.Spec.Payload, replacement) {
				t.Fatalf("expired reauth reset: %+v %v", fresh, err)
			}
			component := before.Spec.Components[0]
			component.ReceivedParts, component.ReceivedBytes = 0, 0
			if fresh.ID != before.ID || fresh.OwnerID != before.OwnerID || fresh.Spec.DeviceID != before.Spec.DeviceID || fresh.Spec.Components[0] != component {
				t.Fatal("reauth changed immutable identity")
			}
			noPayload(t, res)
			want, err := reservationBytes(fresh)
			if err != nil || reserved(t, q) != uint64(want) {
				t.Fatalf("restart reservation: %d %v", reserved(t, q), err)
			}
			spec := specFor(data)
			spec.Payload = replacement
			replay, err := m.Create(res, owner, sessionID, spec)
			if err != nil || replay.Status != "uploading" {
				t.Fatalf("fresh grant create replay: %+v %v", replay, err)
			}
			appendPart(t, m, res, data, 0)
		})
	}
}

func TestExpiredRebindAdmissionAndFutureVersionPreserveFiles(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("must-preserve-on-admission-failure")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	s := diskSession(t, res)
	now := s.UpdatedAt.Add(Lifetime + time.Hour)
	m.now = func() time.Time { return now }
	files := snapshotSession(t, keyFor(res, sessionID))
	// A failed filesystem quota check must precede any payload removal or save.
	m.quota = quota.New(filepath.Join(t.TempDir(), "missing-filesystem"))
	replacement := json.RawMessage(`{"grant":"fresh"}`)
	if err := m.RebindPayload(res, sessionID, device, s.Spec.Payload, replacement); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("admission failure: %v", err)
	}
	unchangedSessionFiles(t, files, snapshotSession(t, keyFor(res, sessionID)))
	if reserved(t, q) != 0 {
		t.Fatal("expired staging retained old workspace")
	}
	m.quota = q
	s.Version = 2
	if err := writeSealed(res, filepath.Join(keyFor(res, sessionID), "session.enc"), s); err != nil {
		t.Fatal(err)
	}
	files = snapshotSession(t, keyFor(res, sessionID))
	if err := m.RebindPayload(res, sessionID, device, s.Spec.Payload, replacement); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("future rebind: %v", err)
	}
	unchangedSessionFiles(t, files, snapshotSession(t, keyFor(res, sessionID)))
}

func unchangedSessionFiles(t *testing.T, before, after map[string]fileSnapshot) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatal("session files changed")
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok || !bytes.Equal(want.body, got.body) || want.mode != got.mode || !want.modified.Equal(got.modified) {
			t.Fatalf("session file mutated: %s", path)
		}
	}
}
