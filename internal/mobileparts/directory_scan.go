package mobileparts

import (
	"errors"
	"io"
	"os"
)

const maxDirectoryScans = 16

type directoryScan struct {
	file *os.File
	used uint64
}

// ReadDir(n) keeps enumeration memory bounded and retains directory position
// between rounds. The LRU caps open directory handles across all owners.
func (m *Manager) readDirectory(path string, limit int) ([]os.DirEntry, bool, error) {
	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	scan := m.scans[path]
	if scan == nil {
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if len(m.scans) >= maxDirectoryScans {
			var oldest string
			var used uint64 = ^uint64(0)
			for key, s := range m.scans {
				if s.used < used {
					oldest, used = key, s.used
				}
			}
			_ = m.scans[oldest].file.Close()
			delete(m.scans, oldest)
		}
		scan = &directoryScan{file: f}
		m.scans[path] = scan
	}
	m.scanTick++
	scan.used = m.scanTick
	entries, err := scan.file.ReadDir(limit)
	eof := errors.Is(err, io.EOF)
	if err != nil {
		_ = scan.file.Close()
		delete(m.scans, path)
	}
	if eof {
		err = nil
	}
	return entries, eof, err
}

// Marker scans wrap at EOF in the same call, so a small persistent queue is
// visible every round without sacrificing the per-call enumeration bound.
func (m *Manager) readMarkers(path string, limit int) ([]os.DirEntry, error) {
	entries, eof, err := m.readDirectory(path, limit)
	if err == nil && eof && len(entries) == 0 {
		entries, _, err = m.readDirectory(path, limit)
	}
	return entries, err
}
