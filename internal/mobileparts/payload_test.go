package mobileparts

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRebindPayloadCASPreservesSessionAndProgress(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("same-immutable-original")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	before, err := m.Session(res, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	quotaBefore := reserved(t, q)
	old := append(json.RawMessage(nil), before.Spec.Payload...)
	replacements := []json.RawMessage{json.RawMessage(`{"name":"private-mobile-file","authorization_version":2}`), json.RawMessage(`{"name":"private-mobile-file","authorization_version":3}`)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, replacement := range replacements {
		wg.Add(1)
		go func(payload json.RawMessage) {
			defer wg.Done()
			<-start
			results <- m.RebindPayload(res, sessionID, device, old, payload)
		}(replacement)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS results: success=%d conflict=%d", success, conflict)
	}
	after, err := m.Session(res, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Key != before.Key || after.OwnerID != before.OwnerID || after.Spec.DeviceID != before.Spec.DeviceID || after.Spec.Components[0] != before.Spec.Components[0] || after.Status != before.Status || !after.CreatedAt.Equal(before.CreatedAt) || reserved(t, q) != quotaBefore {
		t.Fatal("reauth changed session identity, progress, or reservation")
	}
	if bytes.Equal(after.Spec.Payload, old) {
		t.Fatal("reauth did not replace private payload")
	}
	// A stale caller cannot overwrite the admitted epoch, even with equal JSON
	// represented using different whitespace.
	spacedOld := append(json.RawMessage(" "), after.Spec.Payload...)
	if err = m.RebindPayload(res, sessionID, device, spacedOld, old); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonexact CAS: %v", err)
	}
	fresh := json.RawMessage(`{"name":"private-mobile-file","authorization_version":4}`)
	if err = m.RebindPayload(res, sessionID, device, after.Spec.Payload, fresh); err != nil {
		t.Fatal(err)
	}
	fresh[2] = 'X'
	stored, err := m.Session(res, sessionID)
	if err != nil || !json.Valid(stored.Spec.Payload) || bytes.Equal(stored.Spec.Payload, fresh) {
		t.Fatalf("caller mutated saved payload: %s %v", stored.Spec.Payload, err)
	}
}

func TestRebindPayloadRejectsInvalidDeviceAndTerminalStates(t *testing.T) {
	m, res, _ := fixture(t)
	create(t, m, res, []byte("incomplete"))
	s := diskSession(t, res)
	old := s.Spec.Payload
	replacement := json.RawMessage(`{"grant":"fresh"}`)
	for _, bad := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`"` + strings.Repeat("x", 65536) + `"`)} {
		if err := m.RebindPayload(res, sessionID, device, old, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid payload: %v", err)
		}
	}
	if err := m.RebindPayload(res, sessionID, strings.Repeat("e", 32), old, replacement); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong device: %v", err)
	}
	for _, status := range []string{"verifying", "stored", "cancelled"} {
		s.Status = status
		if err := writeSealed(res, filepath.Join(keyFor(res, sessionID), "session.enc"), s); err != nil {
			t.Fatal(err)
		}
		if err := m.RebindPayload(res, sessionID, device, old, replacement); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s rebind: %v", status, err)
		}
	}
}
