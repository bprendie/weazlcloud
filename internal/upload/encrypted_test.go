package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func encryptedFixture(t *testing.T) (string, *filesvc.Resource, ResourceFor) {
	t.Helper()
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("encrypted-upload"), []byte("encrypted-upload")); err != nil {
		t.Fatal(err)
	}
	res := &filesvc.Resource{Vault: v}
	resolve := func(u users.User) *filesvc.Resource {
		if u.ID != testOwner().ID {
			return nil
		}
		return res
	}
	return filepath.Join(root, "uploads"), res, resolve
}

func TestEncryptedOrderedUploadRestartAndNoPlaintext(t *testing.T) {
	root, _, resolve := encryptedFixture(t)
	m := New(root, resolve, nil, nil)
	owner := testOwner()
	ctx := context.Background()
	body := bytes.Repeat([]byte("PRIVATE-CONTENT-"), 100000)
	v, err := m.Create(owner, "private/name.txt", int64(len(body)), digest(body))
	if err != nil {
		t.Fatal(err)
	}
	split := 700003
	for _, part := range [][]byte{body[:split], body[split:]} {
		v, err = m.Append(ctx, owner, v.ID, v.Offset, int64(len(part)), digest(part), bytes.NewReader(part))
		if err != nil {
			t.Fatal(err)
		}
		m = New(root, resolve, nil, nil)
		v, err = m.Status(owner, v.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if v.Offset != int64(len(body)) || v.Status != "ready" {
		t.Fatalf("restart offset %+v", v)
	}
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if bytes.Contains(raw, []byte("PRIVATE-CONTENT")) || bytes.Contains(raw, []byte("private/name")) || bytes.Contains(raw, []byte(digest(body))) {
			t.Fatalf("plaintext in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	v, err = m.Finalize(ctx, owner, v.ID, func(_ context.Context, _ SessionView, r io.Reader) error {
		calls++
		got, err := io.ReadAll(r)
		if !bytes.Equal(got, body) {
			t.Fatal("stream changed bytes")
		}
		return err
	})
	if err != nil || v.Status != "complete" {
		t.Fatalf("finalize %+v %v", v, err)
	}
	_, err = m.Finalize(ctx, owner, v.ID, func(context.Context, SessionView, io.Reader) error { calls++; return nil })
	if err != nil || calls != 1 {
		t.Fatalf("duplicate commit %d %v", calls, err)
	}
	if _, err := os.Stat(m.partPath(owner.ID, v.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("encrypted segments not cleaned")
	}
}

func TestEncryptedWrongChunkLockAndTamper(t *testing.T) {
	root, res, resolve := encryptedFixture(t)
	m := New(root, resolve, nil, nil)
	owner := testOwner()
	ctx := context.Background()
	v, err := m.Create(owner, "secret.bin", 4, digest([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Append(ctx, owner, v.ID, 0, 4, digest([]byte("data")), bytes.NewReader([]byte("oops"))); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("bad checksum: %v", err)
	}
	v, err = m.Status(owner, v.ID)
	if err != nil || v.Offset != 0 {
		t.Fatalf("bad chunk advanced %+v %v", v, err)
	}
	v, err = m.Append(ctx, owner, v.ID, 0, 4, digest([]byte("data")), bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	res.Vault.Lock()
	if _, err := m.Status(owner, v.ID); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked manifest: %v", err)
	}
	if _, _, err := m.SweepExpiredDetailed(ctx, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatalf("locked sweep: %v", err)
	}
	if _, err := os.Stat(m.manifestPath(owner.ID, v.ID)); err != nil {
		t.Fatal("locked session swept")
	}
	if err := res.Vault.UnlockNode(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s, err := m.readSessionLocked(owner.ID, v.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p := m.segmentPath(s, 0)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := m.Finalize(ctx, owner, v.ID, func(context.Context, SessionView, io.Reader) error { called = true; return nil }); err == nil || called {
		t.Fatalf("tamper committed: %v", err)
	}
	if err := m.DeleteOwner(owner.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRawLegacySessionDrainsBesideEncryptedNewSessions(t *testing.T) {
	root, _, resolve := encryptedFixture(t)
	owner := testOwner()
	ctx := context.Background()
	legacy := New(root, nil, nil, nil)
	old, err := legacy.Create(owner, "legacy.bin", 6, digest([]byte("abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Append(ctx, owner, old.ID, 0, 3, digest([]byte("abc")), bytes.NewReader([]byte("abc"))); err != nil {
		t.Fatal(err)
	}
	modern := New(root, resolve, nil, nil)
	old, err = modern.Status(owner, old.ID)
	if err != nil || old.Offset != 3 {
		t.Fatalf("legacy resume %+v %v", old, err)
	}
	if _, err := modern.Append(ctx, owner, old.ID, 3, 3, digest([]byte("def")), bytes.NewReader([]byte("def"))); err != nil {
		t.Fatal(err)
	}
	if _, err := modern.Finalize(ctx, owner, old.ID, func(_ context.Context, _ SessionView, r io.Reader) error {
		got, err := io.ReadAll(r)
		if string(got) != "abcdef" {
			t.Fatal("legacy bytes lost")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	newer, err := modern.Create(owner, "new.bin", 0, digest(nil))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := modern.isEncrypted(owner.ID, newer.ID)
	if err != nil || !encrypted {
		t.Fatal("new session used raw drain format")
	}
	if _, err := modern.Finalize(ctx, owner, newer.ID, func(_ context.Context, _ SessionView, r io.Reader) error {
		got, err := io.ReadAll(r)
		if len(got) != 0 {
			t.Fatal("nonempty zero upload")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
