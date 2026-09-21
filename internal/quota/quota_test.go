package quota

import "testing"

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
