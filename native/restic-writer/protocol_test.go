package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestManifestBounds(t *testing.T) {
	valid := manifest{Version: 1, Files: []entry{testEntry(0, 1)}}
	b, _ := json.Marshal(valid)
	if _, err := readManifest(strings.NewReader(string(b))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*manifest)
	}{
		{"version", func(m *manifest) { m.Version = 0 }},
		{"empty", func(m *manifest) { m.Files = nil }},
		{"nine", func(m *manifest) {
			for i := 1; i < 9; i++ {
				m.Files = append(m.Files, testEntry(i, 1))
			}
		}},
		{"duplicate", func(m *manifest) { m.Files = append(m.Files, m.Files[0]) }},
		{"path", func(m *manifest) { m.Files[0].Name = "../" + strings.Repeat("a", 29) }},
		{"missing-size", func(m *manifest) { m.Files[0].Size = nil }},
		{"negative", func(m *manifest) { n := int64(-1); m.Files[0].Size = &n }},
		{"overflow", func(m *manifest) { n := int64(1<<63 - 1); m.Files[0].Size = &n }},
		{"total", func(m *manifest) {
			n := int64(64 << 20)
			m.Files[0].Size = &n
			m.Files = append(m.Files, testEntry(1, 1))
		}},
		{"hash", func(m *manifest) { m.Files[0].SHA256 = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := manifest{Version: 1, Files: append([]entry(nil), valid.Files...)}
			tc.mutate(&m)
			b, _ := json.Marshal(m)
			if _, err := readManifest(strings.NewReader(string(b))); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	for _, bad := range []string{string(b) + "{}", strings.Repeat(" ", 64<<10) + string(b), `{"version":1,"unexpected":true,"files":[]}`} {
		if _, err := readManifest(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted malformed or oversized JSON")
		}
	}
}

type errorSource struct{}

func (errorSource) Read(p []byte) (int, error) {
	p[0] = 1
	return 1, errors.New("private source detail")
}
func (errorSource) Close() error { return nil }

func TestSourceReadErrorIsFatal(t *testing.T) {
	canceled := false
	s := &checkedSource{ReadCloser: errorSource{}, remaining: 1, hash: sha256.New(), cancel: func() { canceled = true }}
	if _, err := io.Copy(io.Discard, s); err != errBatch || !canceled || s.verified {
		t.Fatal("source read error not fatal")
	}
}
