package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func TestEncryptedPendingPromotionRecovery(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "chunk durable", true: "segment promoted"}[renamed], func(t *testing.T) {
			root, _, resolve := encryptedFixture(t)
			m := New(root, resolve, nil, nil)
			owner := testOwner()
			body := []byte("recover encrypted chunk")
			v, err := m.Create(owner, "resume.bin", int64(len(body)), digest(body))
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			s, err := m.readSessionLocked(owner.ID, v.ID)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			key, err := segmentKey(s, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer cryptox.Zero(key)
			file, err := os.Create(m.chunkPath(owner.ID, v.ID))
			if err != nil {
				t.Fatal(err)
			}
			writer, err := cryptox.NewStreamFileWriter(file, key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(body); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			s.PendingHash = digest(body)
			s.PendingSize = int64(len(body))
			m.mu.Lock()
			err = m.writeLocked(s)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if renamed {
				if err := os.Rename(m.chunkPath(owner.ID, v.ID), m.segmentPath(s, 0)); err != nil {
					t.Fatal(err)
				}
			}
			m = New(root, resolve, nil, nil)
			recovered, err := m.Status(owner, v.ID)
			if err != nil || recovered.Offset != int64(len(body)) || len(recovered.ChunkHashes) != 1 {
				t.Fatalf("pending recovery %+v %v", recovered, err)
			}
			if _, err := m.Finalize(context.Background(), owner, v.ID, func(_ context.Context, _ SessionView, r io.Reader) error {
				got, err := io.ReadAll(r)
				if !bytes.Equal(got, body) {
					t.Fatal("recovery changed bytes")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type encryptedFailReader struct{ done bool }

func (r *encryptedFailReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("transfer interrupted")
	}
	r.done = true
	return copy(p, []byte("partial")), nil
}
func TestEncryptedPartialWriteRetainsOffset(t *testing.T) {
	root, _, resolve := encryptedFixture(t)
	m := New(root, resolve, nil, nil)
	owner := testOwner()
	ctx := context.Background()
	v, err := m.Create(owner, "partial.bin", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Append(ctx, owner, v.ID, 0, -1, digest([]byte("partial")), &encryptedFailReader{}); err == nil {
		t.Fatal("interrupted transfer accepted")
	}
	status, err := m.Status(owner, v.ID)
	if err != nil || status.Offset != 0 {
		t.Fatalf("partial advanced %+v %v", status, err)
	}
	if _, err := os.Stat(m.chunkPath(owner.ID, v.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed chunk retained")
	}
	if err := m.Cancel(owner, v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFutureUploadFormatsFailClosedBeforePayloadWrites(t *testing.T) {
	for _, kind := range []string{"raw-format-3", "sealed-format-3", "magic-v3"} {
		t.Run(kind, func(t *testing.T) {
			root, res, resolve := encryptedFixture(t)
			m := New(root, resolve, nil, nil)
			owner := testOwner()
			v, err := m.Create(owner, "private.bin", 1, digest([]byte("x")))
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			s, err := m.readSessionLocked(owner.ID, v.ID)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			s.Format = 3
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "sealed-format-3" {
				wrapped, err := res.Vault.Wrap(raw)
				if err != nil {
					t.Fatal(err)
				}
				raw = append([]byte(encryptedManifestMagic), wrapped...)
			}
			if kind == "magic-v3" {
				raw = append([]byte("WZU3\n"), raw...)
			}
			if err := os.WriteFile(m.manifestPath(owner.ID, v.ID), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Status(owner, v.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("future status format accepted: %v", err)
			}
			if _, err := m.Append(context.Background(), owner, v.ID, 0, 1, digest([]byte("x")), bytes.NewReader([]byte("x"))); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("future format wrote bytes: %v", err)
			}
			after, err := os.ReadFile(m.manifestPath(owner.ID, v.ID))
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("future manifest was rewritten")
			}
			segments, err := os.ReadDir(m.partPath(owner.ID, v.ID))
			if err != nil || len(segments) != 0 {
				t.Fatal("future format gained payload")
			}
		})
	}
}
