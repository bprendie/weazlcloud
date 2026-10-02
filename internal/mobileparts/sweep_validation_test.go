package mobileparts

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSweepPreservesExpiredFutureSession(t *testing.T) {
	assertSweepPreservesInvalid(t, func(s *Session) { s.Version = 2 })
}

func TestSweepPreservesInvalidExpiredSession(t *testing.T) {
	cases := map[string]func(*Session){
		"missing-version":    func(s *Session) { s.Version = 0 },
		"wrong-session":      func(s *Session) { s.ID = device },
		"invalid-owner":      func(s *Session) { s.OwnerID = "invalid" },
		"invalid-device":     func(s *Session) { s.Spec.DeviceID = "invalid" },
		"unknown-kind":       func(s *Session) { s.Spec.Kind = "future-kind" },
		"missing-components": func(s *Session) { s.Spec.Components = nil },
		"invalid-size":       func(s *Session) { s.Spec.Components[0].Size = -1 },
		"invalid-hash":       func(s *Session) { s.Spec.Components[0].SHA256 = "bad" },
		"invalid-payload":    func(s *Session) { s.Spec.Payload = []byte(`"` + strings.Repeat("x", 65536) + `"`) },
		"invalid-counters":   func(s *Session) { s.Spec.Components[0].ReceivedParts = -1 },
		"unknown-state":      func(s *Session) { s.Status = "future-state" },
		"invalid-key":        func(s *Session) { s.Key = "invalid" },
		"missing-created":    func(s *Session) { s.CreatedAt = time.Time{} },
		"missing-updated":    func(s *Session) { s.UpdatedAt = time.Time{} },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) { assertSweepPreservesInvalid(t, modify) })
	}
}

func assertSweepPreservesInvalid(t *testing.T, modify func(*Session)) {
	t.Helper()
	m, res, q := fixture(t)
	data := []byte("preserve-encrypted-staged-content")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	s := diskSession(t, res)
	now := s.UpdatedAt.Add(Lifetime + time.Hour)
	m.now = func() time.Time { return now }
	modify(&s)
	dir := keyFor(res, sessionID)
	if err := writeSealed(res, filepath.Join(dir, "session.enc"), s); err != nil {
		t.Fatal(err)
	}
	// A newer writer may own files this implementation does not recognize.
	if err := os.Mkdir(filepath.Join(dir, "future-module"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "future-module", "index.bin"), []byte("future-encrypted-index"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotSession(t, dir)
	quotaBefore := reserved(t, q)
	if err := m.Sweep(res); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid session sweep: %v", err)
	}
	if _, err := m.Session(res, sessionID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid session load: %v", err)
	}
	after := snapshotSession(t, dir)
	if len(before) != len(after) {
		t.Fatalf("files changed: before=%d after=%d", len(before), len(after))
	}
	for path, want := range before {
		got, exists := after[path]
		if !exists || !bytes.Equal(want.body, got.body) || want.mode != got.mode || !want.modified.Equal(got.modified) {
			t.Fatalf("unsupported session file changed or disappeared: %s", path)
		}
	}
	if reserved(t, q) != quotaBefore {
		t.Fatal("invalid session sweep mutated quota reservation")
	}
}

type fileSnapshot struct {
	body     []byte
	mode     fs.FileMode
	modified time.Time
}

func snapshotSession(t *testing.T, dir string) map[string]fileSnapshot {
	t.Helper()
	out := map[string]fileSnapshot{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var body []byte
		if !entry.IsDir() {
			body, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[relative] = fileSnapshot{body, info.Mode(), info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
