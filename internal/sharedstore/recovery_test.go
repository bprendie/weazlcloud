package sharedstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedAndPublishedOperationsRecoverIdempotently(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	var stopAt string
	s, err := Open(root, Options{FailureHook: func(p string) error {
		if p == stopAt {
			return os.ErrClosed
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("durable operation fixture")
	stopAt = "prepared"
	_, err = s.Prepare(ctx, "owner", v, "first", 1, bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected injected failure")
	}
	pending, err := s.Pending(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v, %v", pending, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	stopAt = ""
	s, err = Open(root, Options{FailureHook: func(p string) error {
		if p == stopAt {
			return os.ErrClosed
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Recover(ctx, pending[0], false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Pending(ctx); len(got) != 0 {
		t.Fatalf("aborted operation remains pending: %v", got)
	}
	if freed, collectErr := s.Collect(ctx); collectErr != nil || freed == 0 {
		t.Fatalf("aborted write object was not collectible: bytes=%d err=%v", freed, collectErr)
	}

	stopAt = ""
	p, err := s.Prepare(ctx, "owner", v, "second", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	// This file models the caller durably publishing the encrypted catalog reference.
	manifest := filepath.Join(root, "owner-catalog.enc")
	wrapped, err := v.Wrap([]byte(p.Operation + "|" + p.Reference.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, wrapped, 0o600); err != nil {
		t.Fatal(err)
	}
	stopAt = "published"
	if err = s.MarkPublished(ctx, p.Operation); err == nil {
		t.Fatal("expected injected publication boundary failure")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	stopAt = ""
	s, err = Open(root, Options{FailureHook: func(p string) error {
		if p == stopAt {
			return os.ErrClosed
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	proof, err := v.Unwrap(wrapped)
	if err != nil || string(proof) != p.Operation+"|"+p.Reference.ObjectID {
		t.Fatalf("published catalog proof invalid: %v", err)
	}
	stopAt = "committed"
	if err = s.Recover(ctx, p.Operation, true); err == nil {
		t.Fatal("expected injected post-commit error")
	}
	stopAt = ""
	if err = s.Recover(ctx, p.Operation, true); err != nil {
		t.Fatalf("commit retry not idempotent: %v", err)
	}
	var out bytes.Buffer
	if err = s.Read(ctx, "owner", v, p.Reference, &out); err != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("recovered read %q, %v", out.Bytes(), err)
	}
}

func TestObjectWriteFailurePointsRecover(t *testing.T) {
	for _, point := range []string{"cipher_synced", "object_renamed", "index_ready", "object_ready"} {
		t.Run(point, func(t *testing.T) {
			root := testRoot(t)
			ctx := context.Background()
			v := testVault(t, root, "owner")
			stop := point
			s, err := Open(root, Options{FailureHook: func(p string) error {
				if p == stop {
					return os.ErrClosed
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			data := bytes.Repeat([]byte("retry-safe"), 1000)
			if _, err = s.Prepare(ctx, "owner", v, "entry", 1, bytes.NewReader(data), int64(len(data))); err == nil {
				t.Fatal("expected injected error")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			stop = ""
			s, err = Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, err := s.Prepare(ctx, "owner", v, "entry", 1, bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.MarkPublished(ctx, p.Operation); err != nil {
				t.Fatal(err)
			}
			if err = s.Commit(ctx, p.Operation); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err = s.Read(ctx, "owner", v, p.Reference, &out); err != nil || !bytes.Equal(out.Bytes(), data) {
				t.Fatalf("retry read mismatch: %v", err)
			}
		})
	}
}

func TestRejectedUploadsAndAuthenticatedFrameFailures(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := []byte("must not publish truncated data")
	if _, err = s.Prepare(ctx, "owner", v, "bad", 1, bytes.NewReader(data), int64(len(data)+1)); err == nil {
		t.Fatal("accepted wrong declared length")
	}
	if p, err := s.Pending(ctx); err != nil || len(p) != 0 {
		t.Fatalf("bad source journaled: %v %v", p, err)
	}
	if _, err = s.Prepare(ctx, "owner", v, "interrupted", 1, &errorAfterBytes{}, -1); err == nil {
		t.Fatal("accepted interrupted source stream")
	}
	if n, e := s.ObjectCount(ctx); e != nil || n != 0 {
		t.Fatalf("interrupted source published an object: %d %v", n, e)
	}
	p, err := s.Prepare(ctx, "owner", v, "good", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, p.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, p.Operation); err != nil {
		t.Fatal(err)
	}
	path := s.objectPath(p.Reference.ObjectID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(path, original, 0o600)
	mutated := append([]byte(nil), original...)
	mutated[13+2] ^= 0x40
	if err = os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = s.Read(ctx, "owner", v, p.Reference, &out)
	if err == nil || out.Len() != 0 {
		t.Fatalf("tampered first frame returned %d bytes, %v", out.Len(), err)
	}
	if err = os.WriteFile(path, append(append([]byte(nil), original...), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = s.Read(ctx, "owner", v, p.Reference, &out); err == nil {
		t.Fatal("accepted trailing ciphertext")
	}
}

type errorAfterBytes struct{ sent bool }

func (r *errorAfterBytes) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("injected source failure")
	}
	r.sent = true
	return copy(p, []byte("partial")), nil
}

var _ io.Reader = (*errorAfterBytes)(nil)

func TestOlderReplacementCannotCommitAfterNewerRevision(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.Prepare(ctx, "owner", v, "entry", 1, bytes.NewReader([]byte("old")), 3)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Prepare(ctx, "owner", v, "entry", 2, bytes.NewReader([]byte("new")), 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, newer.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, newer.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, old.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, old.Operation); !errors.Is(err, ErrStale) {
		t.Fatalf("stale revision commit: %v", err)
	}
	if err = s.Recover(ctx, old.Operation, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = s.Read(ctx, "owner", v, newer.Reference, &out); err != nil || out.String() != "new" {
		t.Fatalf("new revision lost: %q %v", out.String(), err)
	}
}
