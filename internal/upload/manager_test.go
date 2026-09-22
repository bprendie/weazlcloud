package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

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
	first, err := manager.Append(context.Background(), owner, created.ID, 0, 5, bytes.NewReader([]byte("hello")))
	if err != nil || first.Offset != 5 {
		t.Fatalf("first chunk view=%+v err=%v", first, err)
	}
	var offsetErr *OffsetError
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, 1, bytes.NewReader([]byte("x"))); !errors.As(err, &offsetErr) {
		t.Fatalf("wrong offset error=%v", err)
	}

	manager = New(root, nil, nil, nil)
	resumed, err := manager.Status(owner, created.ID)
	if err != nil || resumed.Offset != 5 {
		t.Fatalf("restart status=%+v err=%v", resumed, err)
	}
	if _, err := manager.Append(context.Background(), owner, created.ID, 5, -1, bytes.NewReader([]byte(" world"))); err != nil {
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
	if _, err := manager.Append(context.Background(), owner, created.ID, 0, -1, failingReader{}); err == nil {
		t.Fatal("interrupted chunk unexpectedly succeeded")
	}
	view, err := manager.Status(owner, created.ID)
	if err != nil || view.Offset != 0 {
		t.Fatalf("interrupted chunk advanced session: view=%+v err=%v", view, err)
	}
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	copy(p, []byte("partial"))
	return len("partial"), errors.New("connection interrupted")
}
