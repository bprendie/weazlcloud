package quota

import (
	"bytes"
	"io"
	"testing"
)

func TestReserveUsesSharedStorageWithoutUserAllocation(t *testing.T) {
	m := newWithStatfs("test", func(string) (uint64, uint64, error) {
		return 1000, 200, nil
	})
	release, err := m.Reserve("alice", 2, 900, 100, 150)
	if err != nil {
		t.Fatalf("alice should use available shared storage: %v", err)
	}
	release()
	if _, err := m.Reserve("bob", 2, 0, 0, 201); err == nil {
		t.Fatal("reservation should stop at the global usable limit")
	}
}

func TestReserveCountsOverwriteSpoolSpace(t *testing.T) {
	m := newWithStatfs("test", func(string) (uint64, uint64, error) {
		return 1000, 100, nil
	})
	if _, err := m.Reserve("alice", 1, 100, 100, 100); err == nil {
		t.Fatal("same-sized overwrite still needs temporary working space")
	}
}

func TestGuardReaderMultiplierReservesSharedWriteWorkspace(t *testing.T) {
	m := newWithStatfs("test", func(string) (uint64, uint64, error) {
		return 1000, 900, nil
	})
	_, release, err := m.GuardReaderMultiplier("alice", 1, 0, 0, 150, 2, bytes.NewReader(bytes.Repeat([]byte{'x'}, 150)))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	status, err := m.Status(1)
	if err != nil || status.Reserved != 300 {
		t.Fatalf("reserved=%d err=%v", status.Reserved, err)
	}
	if _, _, err = m.GuardReaderMultiplier("bob", 1, 0, 0, 300, 2, bytes.NewReader(nil)); err == nil {
		t.Fatal("shared write reservation must include coexisting source and destination")
	}
	reader, releaseUnknown, err := m.GuardReaderMultiplier("alice", 1, 0, 0, -1, 2, bytes.NewReader(bytes.Repeat([]byte{'y'}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if status, err = m.Status(1); err != nil || status.Reserved != 320 {
		t.Fatalf("unknown-length reservation=%d err=%v", status.Reserved, err)
	}
	releaseUnknown()
}

func TestSharedWorkspaceIncludesMetadataAndResumesFromOffset(t *testing.T) {
	size, offset := int64(2<<20), int64(512<<10)
	reserved, err := SharedWriteReservation(size, offset)
	if err != nil || reserved != 2*size-offset+(1<<20)+4*(8<<10) {
		t.Fatalf("resumed shared reservation=%d err=%v", reserved, err)
	}
	m := newWithStatfs("test", func(string) (uint64, uint64, error) {
		return 32 << 20, 30 << 20, nil
	})
	_, release, err := m.GuardSharedWrite("alice", 1, 0, 0, (512<<10)+1, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	status, err := m.Status(1)
	want := uint64(2*((512<<10)+1) + (1 << 20) + 2*(8<<10))
	if err != nil || status.Reserved != want {
		t.Fatalf("direct shared reservation=%d want=%d err=%v", status.Reserved, want, err)
	}
}
