package sharedstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestCollectorHonorsDurableHoldAndResumesClaim(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Prepare(ctx, "owner", v, "entry", 1, bytes.NewReader([]byte("held")), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, p.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, p.Operation); err != nil {
		t.Fatal(err)
	}
	release, err := s.Hold(ctx, "owner", p.Reference)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Release(ctx, "owner", "entry", 1); err != nil {
		t.Fatal(err)
	}
	if freed, err := s.Collect(ctx); err != nil || freed != 0 {
		t.Fatalf("collector ignored live hold: freed=%d err=%v", freed, err)
	}
	release()
	if freed, err := s.Collect(ctx); err != nil || freed == 0 {
		t.Fatalf("collector failed after release: freed=%d err=%v", freed, err)
	}
	// Leave another unreferenced object at the durable delete-claim boundary.
	second, err := s.Prepare(ctx, "owner", v, "interrupted", 1, bytes.NewReader([]byte("restart")), 7)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, second.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, second.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Release(ctx, "owner", "interrupted", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE objects SET state='deleting' WHERE object_id=?", second.Reference.ObjectID); err != nil {
		t.Fatal(err)
	}
	claimedPath := s.objectPath(second.Reference.ObjectID)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	// Model a process stopping after the durable delete claim but before unlink.
	s, err = Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM objects WHERE object_id=?", second.Reference.ObjectID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claimed object row survived restart: count=%d err=%v", count, err)
	}
	if _, err = os.Stat(claimedPath); !os.IsNotExist(err) {
		t.Fatalf("claimed object file survived restart: %v", err)
	}
	_ = s.Close()
}

func TestReconcileOwnerFinishesPublishedAndRemovesOrphanReferences(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := []byte("catalog-backed")
	published, err := s.Prepare(ctx, "owner", v, "live", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, published.Operation); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, published.Operation); err != nil {
		t.Fatal(err)
	}
	orphan, err := s.Grant(ctx, "owner", v, published.Reference, "orphan-copy", 1)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Prepare(ctx, "owner", v, "interrupted", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileOwner(ctx, "owner", map[string]struct{}{published.Operation: {}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = s.Read(ctx, "owner", v, published.Reference, &out); err != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("catalog reference lost: %v", err)
	}
	if err = s.Read(ctx, "owner", v, orphan, io.Discard); !errors.Is(err, ErrDenied) {
		t.Fatalf("orphan grant still readable: %v", err)
	}
	if err = s.Recover(ctx, pending.Operation, false); !errors.Is(err, ErrState) {
		t.Fatalf("pending op was not reconciled: %v", err)
	}
}

func TestPrepareWithIDRetryCannotChangePayload(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	v := testVault(t, root, "owner")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	op := "0123456789abcdef0123456789abcdef"
	data := []byte("durable retry payload")
	prepared, err := s.PrepareWithID(ctx, op, "owner", v, "entry", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPublished(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, op); err != nil {
		t.Fatal(err)
	}
	retry, err := s.PrepareWithID(ctx, op, "owner", v, "entry", 1, bytes.NewReader(data), int64(len(data)))
	if err != nil || retry.Reference != prepared.Reference {
		t.Fatalf("same-payload retry: %v", err)
	}
	if _, err = s.PrepareWithID(ctx, op, "owner", v, "entry", 1, bytes.NewReader([]byte("different")), 9); !errors.Is(err, ErrState) {
		t.Fatalf("operation ID accepted changed payload: %v", err)
	}
}
