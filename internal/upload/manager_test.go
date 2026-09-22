package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func testOwner() users.User {
	return users.User{ID: "0123456789abcdef0123456789abcdef", Username: "upload"}
}

func TestSessionOffsetsRestartAndIdempotentFinalize(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	owner := testOwner()
	manager := New(root, nil, nil, nil)
	created, err := manager.Create(owner, "folder/disk.iso", 11, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Append(context.Background(), owner, created.ID, 0, 5, digest([]byte("hello")), bytes.NewReader([]byte("hello")))
	if err != nil || first.Offset != 5 {
		t.Fatalf("first chunk view=%+v err=%v", first, err)
	}
	var offsetErr *OffsetError
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, 1, digest([]byte("x")), bytes.NewReader([]byte("x"))); !errors.As(err, &offsetErr) {
		t.Fatalf("wrong offset error=%v", err)
	}

	manager = New(root, nil, nil, nil)
	resumed, err := manager.Status(owner, created.ID)
	if err != nil || resumed.Offset != 5 {
		t.Fatalf("restart status=%+v err=%v", resumed, err)
	}
	if _, err := manager.Append(context.Background(), owner, created.ID, 5, -1, digest([]byte(" world")), bytes.NewReader([]byte(" world"))); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	finished, err := manager.Finalize(context.Background(), owner, created.ID, func(_ context.Context, view SessionView, body io.Reader) error {
		var readErr error
		stored, readErr = io.ReadAll(body)
		if readErr == nil && view.Offset != view.Size {
			return errors.New("finalize view was not complete")
		}
		return readErr
	})
	if err != nil || finished.Status != "complete" || !bytes.Equal(stored, []byte("hello world")) {
		t.Fatalf("finalize view=%+v stored=%q err=%v", finished, stored, err)
	}
	called := false
	repeated, err := manager.Finalize(context.Background(), owner, created.ID, func(context.Context, SessionView, io.Reader) error {
		called = true
		return errors.New("duplicate commit")
	})
	if err != nil || repeated.Status != "complete" || called {
		t.Fatalf("repeated finalize view=%+v err=%v called=%v", repeated, err, called)
	}
}

func TestInterruptedChunkDoesNotAdvanceOffset(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "uploads"), nil, nil, nil)
	owner := testOwner()
	created, err := manager.Create(owner, "partial.bin", 9, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, -1, digest([]byte("partial")), failingReader{}); err == nil {
		t.Fatal("interrupted chunk unexpectedly succeeded")
	}
	view, err := manager.Status(owner, created.ID)
	if err != nil || view.Offset != 0 {
		t.Fatalf("interrupted chunk advanced session: view=%+v err=%v", view, err)
	}
}

func TestChunkHashMismatchAndExpiryCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	manager := New(root, nil, nil, nil)
	owner := testOwner()
	created, err := manager.Create(owner, "old.bin", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, 3, digest([]byte("bad")), bytes.NewReader([]byte("yes"))); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("mismatched chunk hash error=%v", err)
	}
	status, err := manager.Status(owner, created.ID)
	if err != nil || status.Offset != 0 {
		t.Fatalf("mismatched chunk advanced session: %+v, %v", status, err)
	}
	b, err := os.ReadFile(manager.manifestPath(owner.ID, created.ID))
	if err != nil {
		t.Fatal(err)
	}
	var saved session
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	saved.UpdatedAt = time.Now().Add(-SessionLifetime - time.Minute)
	b, _ = json.Marshal(saved)
	if err := os.WriteFile(manager.manifestPath(owner.ID, created.ID), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if removed := manager.SweepExpired(time.Now()); removed != 1 {
		t.Fatalf("expired sessions removed=%d, want 1", removed)
	}
	if _, err := os.Stat(manager.partPath(owner.ID, created.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired spool remains: %v", err)
	}
}

func TestQuotaReservationRestoresAndReleases(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(data, "uploads")
	owner := testOwner()
	manager := New(root, nil, quota.New(data), func() int { return 1 })
	created, err := manager.Create(owner, "large.iso", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("12345")
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, int64(len(first)), digest(first), bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	manager = New(root, nil, quota.New(data), func() int { return 1 })
	status, err := manager.quota.Status(1)
	if err != nil || status.Reserved == 0 {
		t.Fatalf("restart failed to reconstruct upload reservation: %+v, %v", status, err)
	}
	if err := manager.Cancel(owner, created.ID); err != nil {
		t.Fatal(err)
	}
	status, err = manager.quota.Status(1)
	if err != nil || status.Reserved != 0 {
		t.Fatalf("cancel did not release reservation: %+v, %v", status, err)
	}
}

func TestDifferentSessionsAppendWithoutGlobalSerialization(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "uploads"), nil, nil, nil)
	owner := testOwner()
	first, err := manager.Create(owner, "first.bin", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(owner, "second.bin", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	reader := &waitReader{entered: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Append(context.Background(), owner, first.ID, 0, 4, digest([]byte("one!")), reader)
		firstDone <- err
	}()
	<-reader.entered
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Append(context.Background(), owner, second.ID, 0, 4, digest([]byte("two!")), bytes.NewReader([]byte("two!")))
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(reader.release)
		t.Fatal("another upload session blocked behind a slow chunk")
	}
	close(reader.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

type waitReader struct {
	entered chan struct{}
	release chan struct{}
	sent    bool
}

func (r *waitReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	close(r.entered)
	<-r.release
	return copy(p, []byte("one!")), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	copy(p, []byte("partial"))
	return len("partial"), errors.New("connection interrupted")
}
