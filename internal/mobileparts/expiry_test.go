package mobileparts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPollingRestartExpiryAndSafeRecreation(t *testing.T) {
	m, res, q := fixture(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	data := []byte("retained-encrypted-part")
	first := create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	before := diskSession(t, res)
	m.ReleaseOwner(owner)
	restart := New(q)
	t.Cleanup(func() { restart.ReleaseOwner(owner) })
	restart.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		now = now.Add(5 * time.Hour)
		got, err := restart.Status(res, sessionID, device)
		if err != nil || !got.UpdatedAt.Equal(before.UpdatedAt) || !got.ExpiresAt.Equal(first.ExpiresAt) {
			t.Fatalf("poll refreshed inactivity: %+v %v", got, err)
		}
		pending, err := restart.Pending(res)
		if err != nil || len(pending) != 1 || !pending[0].UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("worker poll: %+v %v", pending, err)
		}
	}
	now = now.Add(4 * time.Hour)
	if _, err := restart.Status(res, sessionID, device); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry: %v", err)
	}
	if reserved(t, q) != 0 {
		t.Fatal("expiry retained reservation")
	}
	different := specFor(data)
	different.Payload = []byte(`{"name":"another"}`)
	if _, err := restart.Create(res, owner, sessionID, different); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired identity replaced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keyFor(res, sessionID), partName("original", 0)+".wza")); err != nil {
		t.Fatal("conflict removed original payload")
	}
	fresh, err := restart.Create(res, owner, sessionID, specFor(data))
	if err != nil || fresh.Status != "uploading" || fresh.Components[0].ReceivedParts != 0 {
		t.Fatalf("recreation: %+v %v", fresh, err)
	}
	after := diskSession(t, res)
	if after.Key == before.Key || !after.CreatedAt.Equal(now) {
		t.Fatal("recreation reused key or age")
	}
	noPayload(t, res)
	now = now.Add(Lifetime)
	if err = restart.Sweep(res); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(keyFor(res, sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired sweep: %v", err)
	}
	if reserved(t, q) != 0 {
		t.Fatal("sweep leaked quota")
	}
}

func TestUnknownSessionGatesAreNotRetained(t *testing.T) {
	m, res, _ := fixture(t)
	for i := 0; i < 500; i++ {
		id := strings.Repeat("0", 28) + fmt.Sprintf("%04x", i)
		if _, err := m.Session(res, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing session: %v", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.gates) != 0 || len(m.checked) != 0 {
		t.Fatal("unknown sessions retained gates")
	}
}
