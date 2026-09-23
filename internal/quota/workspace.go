package quota

import (
	"io"
	"math"
)

const (
	sharedWorkspaceFixed    = 1 << 20
	sharedWorkspacePerChunk = 8 << 10
	sharedWorkspaceChunk    = 512 << 10
)

// SharedWriteWorkspace reserves SQLite/WAL headroom and worst-case per-chunk
// index, manifest, and filesystem allocation overhead.
func SharedWriteWorkspace(size int64) (int64, error) {
	return workspaceOverhead(size, sharedWorkspaceFixed, sharedWorkspacePerChunk, sharedWorkspaceChunk)
}

// SharedWriteReservation includes source/destination coexistence and metadata.
func SharedWriteReservation(size, offset int64) (int64, error) {
	if size < 0 || offset < 0 || offset > size || size > math.MaxInt64/2 {
		return 0, ErrExceeded
	}
	base := 2*size - offset
	overhead, err := SharedWriteWorkspace(size)
	if err != nil || base > math.MaxInt64-overhead {
		return 0, ErrExceeded
	}
	return base + overhead, nil
}

// GuardSharedWrite applies the same bound to direct PUT, including unknown lengths.
func (m *Manager) GuardSharedWrite(userID string, users int, userUsed, current, expected int64, src io.Reader) (io.Reader, func(), error) {
	return m.GuardReaderWorkspace(userID, users, userUsed, current, expected, 2, sharedWorkspaceFixed, sharedWorkspacePerChunk, sharedWorkspaceChunk, src)
}
