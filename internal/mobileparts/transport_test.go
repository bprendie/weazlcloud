package mobileparts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestOutOfOrderIdempotencyConflictAndQuota(t *testing.T) {
	m, res, q := fixture(t)
	data := append(bytes.Repeat([]byte("private-mobile-content"), int(PartSize)/22+1)[:PartSize], []byte("tail")...)
	spec := specFor(data)
	spec.Components[0].ReceivedParts = 99
	spec.Components[0].SHA256 = strings.ToUpper(spec.Components[0].SHA256)
	original := spec.Components[0]
	if _, err := m.Create(res, owner, sessionID, spec); err != nil {
		t.Fatal(err)
	}
	if spec.Components[0] != original {
		t.Fatal("Create mutated caller components")
	}
	initial := reserved(t, q)
	got := appendPart(t, m, res, data, 1)
	if got.Components[0].ReceivedParts != 1 || got.Components[0].ReceivedBytes != 4 || got.Status != "uploading" {
		t.Fatalf("out of order counters: %+v", got)
	}
	if reserved(t, q) != initial-4-8192 {
		t.Fatal("quota did not shrink by staged part and overhead")
	}
	retry, err := m.Create(res, owner, sessionID, spec)
	if err != nil || retry.Components[0].ReceivedParts != 1 {
		t.Fatalf("Create retry reset progress: %+v %v", retry, err)
	}
	duplicate := appendPart(t, m, res, data, 1)
	if duplicate.Components[0] != got.Components[0] || !duplicate.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatal("duplicate changed counters/activity")
	}
	_, err = m.Append(context.Background(), res, sessionID, device, "original", 1, 4, digest([]byte("evil")), bytes.NewReader([]byte("evil")))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting duplicate: %v", err)
	}
	_, err = m.Append(context.Background(), res, sessionID, device, "original", 0, PartSize, digest(data[:PartSize]), io.LimitReader(bytes.NewReader(data), PartSize-1))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("short body: %v", err)
	}
	_, err = m.Append(context.Background(), res, sessionID, device, "original", 0, PartSize, digest([]byte("wrong")), bytes.NewReader(data[:PartSize]))
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("part checksum: %v", err)
	}
	missing, err := m.Missing(res, sessionID, device, "original", 0, 1)
	if err != nil || len(missing.Missing) != 1 || missing.Missing[0] != 0 || !missing.HasMore {
		t.Fatalf("missing page: %+v %v", missing, err)
	}
	complete := appendPart(t, m, res, data, 0)
	if complete.Status != "queued" || complete.Components[0].ReceivedBytes != int64(len(data)) {
		t.Fatalf("complete: %+v", complete)
	}
	s := diskSession(t, res)
	want, err := reservationBytes(s)
	if err != nil || reserved(t, q) != uint64(want) {
		t.Fatalf("remaining commit quota: %d %v", reserved(t, q), err)
	}
	r, err := m.Open(context.Background(), res, sessionID, "original")
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("ordered read: %v", err)
	}
	if err = m.Verify(context.Background(), res, sessionID); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(keyFor(res, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		path := filepath.Join(keyFor(res, sessionID), entry.Name())
		disk, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(disk, []byte("private-mobile-content")) || bytes.Contains(disk, []byte("private-mobile-file")) || bytes.Contains(disk, []byte(s.Key)) {
			t.Fatalf("plaintext staging: %s", entry.Name())
		}
		if strings.HasPrefix(entry.Name(), ".incoming-") {
			t.Fatal("failed append left incoming file")
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("staging permissions: %v", err)
		}
	}
}

func TestWholeChecksumLockTamperAndCancellation(t *testing.T) {
	m, res, q := fixture(t)
	data := bytes.Repeat([]byte("secret-mobile-original"), 100000)
	spec := specFor(data)
	spec.Components[0].SHA256 = digest([]byte("different component"))
	if _, err := m.Create(res, owner, sessionID, spec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Append(ctx, res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(data))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled append: %v", err)
	}
	current, err := m.Status(res, sessionID, device)
	if err != nil || current.Components[0].ReceivedBytes != 0 {
		t.Fatalf("cancel counters: %+v %v", current, err)
	}
	appendPart(t, m, res, data, 0)
	if err = m.Verify(context.Background(), res, sessionID); !errors.Is(err, ErrChecksum) {
		t.Fatalf("whole checksum: %v", err)
	}
	r, err := m.Open(context.Background(), res, sessionID, "original")
	if err != nil {
		t.Fatal(err)
	}
	res.Vault.Lock()
	if _, err = r.Read(make([]byte, 10)); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked reader: %v", err)
	}
	r.Close()
	if _, err = m.Status(res, sessionID, device); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked session: %v", err)
	}
	m.ReleaseOwner(owner)
	if reserved(t, q) != 0 {
		t.Fatal("owner lock leaked reservation")
	}
	if err = res.Vault.Unlock([]byte("test-password")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(keyFor(res, sessionID), partName("original", 0)+".wza")
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	disk[30] ^= 1
	if err = os.WriteFile(path, disk, 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(context.Background(), res, sessionID); !errors.Is(err, cryptox.ErrStreamFile) {
		t.Fatalf("tamper: %v", err)
	}
	if err = m.Cancel(res, sessionID, device); err != nil {
		t.Fatal(err)
	}
	noPayload(t, res)
	if reserved(t, q) != 0 {
		t.Fatal("cancel leaked quota")
	}
}
