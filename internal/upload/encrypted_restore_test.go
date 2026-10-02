package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestRestoreOwnerContinuesPastBadEncryptedNeighbors(t *testing.T) {
	root, res, resolve := encryptedFixture(t)
	owner := testOwner()
	m := New(root, resolve, quota.New(filepath.Dir(root)), func() int { return 1 })
	good, err := m.Create(owner, "valid.bin", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("valid")
	good, err = m.Append(context.Background(), owner, good.ID, 0, int64(len(body)), digest(body), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	template, err := m.readSessionLocked(owner.ID, good.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string][]byte)
	// These IDs sort before the random valid ID, exposing fail-fast restoration.
	for i, kind := range []string{"future", "corrupt", "over-quota"} {
		id := []string{"00000000000000000000000000000001", "00000000000000000000000000000002", "00000000000000000000000000000003"}[i]
		s := template
		s.ID = id
		s.Offset = 0
		s.Parts = nil
		s.ChunkHashes = nil
		s.Status = "uploading"
		if kind == "future" {
			s.Format = 3
		}
		if kind == "over-quota" {
			s.Size = math.MaxInt64 / 2
		}
		plain, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := res.Vault.Wrap(plain)
		if err != nil {
			t.Fatal(err)
		}
		raw := append([]byte(encryptedManifestMagic), wrapped...)
		if kind == "corrupt" {
			raw = []byte(encryptedManifestMagic + "corrupt")
		}
		manifest := m.manifestPath(owner.ID, id)
		if err := os.WriteFile(manifest, raw, 0600); err != nil {
			t.Fatal(err)
		}
		snapshots[manifest] = raw
		if err := os.Mkdir(m.partPath(owner.ID, id), 0700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(m.partPath(owner.ID, id), "preserve")
		if err := os.WriteFile(marker, []byte("retained payload"), 0600); err != nil {
			t.Fatal(err)
		}
		snapshots[marker] = []byte("retained payload")
	}
	res.Vault.Lock()
	q := quota.New(filepath.Dir(root))
	restarted := New(root, resolve, q, func() int { return 1 })
	if err := res.Vault.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	err = restarted.RestoreOwner(owner)
	if !errors.Is(err, ErrCorrupt) || !errors.Is(err, vault.ErrPass) || !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("bad-neighbor errors not aggregated: %v", err)
	}
	want, err := encryptedReservation(good.Size, 1)
	if err != nil {
		t.Fatal(err)
	}
	status, err := q.Status(1)
	if err != nil || status.Reserved != uint64(want) {
		t.Fatalf("valid reservation not restored: %+v %v want=%d", status, err, want)
	}
	views, err := restarted.List(owner)
	if err == nil || len(views) != 1 || views[0].ID != good.ID || views[0].Offset != good.Offset {
		t.Fatalf("valid partial results lost: %+v %v", views, err)
	}
	for p, before := range snapshots {
		after, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("bad-neighbor state mutated: %s %v", p, err)
		}
	}
}
