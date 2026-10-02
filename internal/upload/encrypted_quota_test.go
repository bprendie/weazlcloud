package upload

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestEncryptedPartialReservationMatchesRestart(t *testing.T) {
	for _, name := range []string{"unlocked startup", "locked startup then status", "locked startup then RestoreOwner"} {
		lockedStartup := name != "unlocked startup"
		t.Run(name, func(t *testing.T) {
			root, res, resolve := encryptedFixture(t)
			q := quota.New(filepath.Dir(root))
			m := New(root, resolve, q, func() int { return 1 })
			owner := testOwner()
			body := bytes.Repeat([]byte("quota-accounting-"), 4096)
			v, err := m.Create(owner, "partial.iso", int64(len(body)*2), "")
			if err != nil {
				t.Fatal(err)
			}
			initial, err := q.Status(1)
			if err != nil {
				t.Fatal(err)
			}
			v, err = m.Append(context.Background(), owner, v.ID, 0, int64(len(body)), digest(body), bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			before, err := q.Status(1)
			if err != nil {
				t.Fatal(err)
			}
			want, err := encryptedReservation(v.Size, 1)
			if err != nil {
				t.Fatal(err)
			}
			if before.Reserved != uint64(want) || before.Reserved != initial.Reserved+(8<<10) {
				t.Fatalf("partial acceptance changed conservative reservation: initial=%d accepted=%d want=%d", initial.Reserved, before.Reserved, want)
			}
			if lockedStartup {
				res.Vault.Lock()
			}
			restartQuota := quota.New(filepath.Dir(root))
			restarted := New(root, resolve, restartQuota, func() int { return 1 })
			if lockedStartup {
				if _, err := restarted.Status(owner, v.ID); !errors.Is(err, vault.ErrLocked) {
					t.Fatalf("locked status: %v", err)
				}
				if err := res.Vault.UnlockNode(); err != nil {
					t.Fatal(err)
				}
				if name == "locked startup then RestoreOwner" {
					// The parent unlock hook restores immediately, before client status.
					if err := restarted.RestoreOwner(owner); err != nil {
						t.Fatal(err)
					}
					unlocked, err := restartQuota.Status(1)
					if err != nil || unlocked.Reserved != before.Reserved {
						t.Fatalf("unlock restoration before status: %+v %v", unlocked, err)
					}
				}
			}
			recovered, err := restarted.Status(owner, v.ID)
			if err != nil || recovered.Offset != v.Offset {
				t.Fatalf("restored offset: %+v %v", recovered, err)
			}
			after, err := restartQuota.Status(1)
			if err != nil {
				t.Fatal(err)
			}
			if after.Reserved != before.Reserved {
				t.Fatalf("restart changed reservation: before=%d after=%d", before.Reserved, after.Reserved)
			}
			if err := restarted.Cancel(owner, v.ID); err != nil {
				t.Fatal(err)
			}
			released, err := restartQuota.Status(1)
			if err != nil || released.Reserved != 0 {
				t.Fatalf("cancel leaked reservation: %+v %v", released, err)
			}
		})
	}
}
