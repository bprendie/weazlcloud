package mobileparts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var owner = strings.Repeat("a", 32)
var device = strings.Repeat("b", 32)
var sessionID = strings.Repeat("c", 32)

func fixture(t *testing.T) (*Manager, *filesvc.Resource, *quota.Manager) {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("test-password"), []byte("test-password")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Lock)
	res := &filesvc.Resource{Vault: v, Lib: library.New(filepath.Join(dir, "repo"), filepath.Join(dir, "catalog.enc"), v)}
	q := quota.New(dir)
	m := New(q)
	t.Cleanup(func() { m.ReleaseOwner(owner) })
	return m, res, q
}
func digest(p []byte) string { hash := sha256.Sum256(p); return hex.EncodeToString(hash[:]) }
func specFor(data []byte) Spec {
	return Spec{Kind: "file", DeviceID: device, Components: []Component{{ID: "original", Size: int64(len(data)), SHA256: digest(data)}}, CommitWhenComplete: true, Payload: json.RawMessage(`{"name":"private-mobile-file"}`)}
}
func create(t *testing.T, m *Manager, res *filesvc.Resource, data []byte) View {
	t.Helper()
	v, err := m.Create(res, owner, sessionID, specFor(data))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func appendPart(t *testing.T, m *Manager, res *filesvc.Resource, data []byte, index int64) View {
	t.Helper()
	start := index * PartSize
	part := data[start:min(start+PartSize, int64(len(data)))]
	v, err := m.Append(context.Background(), res, sessionID, device, "original", index, int64(len(part)), digest(part), bytes.NewReader(part))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func reserved(t *testing.T, q *quota.Manager) uint64 {
	t.Helper()
	s, err := q.Status(1)
	if err != nil {
		t.Fatal(err)
	}
	return s.Reserved
}
func diskSession(t *testing.T, res *filesvc.Resource) Session {
	t.Helper()
	var s Session
	if err := readSealed(res, filepath.Join(keyFor(res, sessionID), "session.enc"), &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func noPayload(t *testing.T, res *filesvc.Resource) {
	t.Helper()
	entries, err := os.ReadDir(keyFor(res, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.enc" {
			t.Fatalf("payload remains: %s", e.Name())
		}
	}
}
