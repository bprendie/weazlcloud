package mobileparts

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

// Call only for a genuinely new session under admissionMu. Durable .live headers
// reconstruct count/byte admission after restart. Existing sessions bypass this
// limit, so pressure never strands accepted work. No catalog is read.
func (m *Manager) admitNew(res *filesvc.Resource, spec Spec) error {
	limits := m.ReceiveLimits()
	f, err := os.Open(filepath.Join(Root(res), ".live"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	n := 0
	var bytes int64
	for {
		entries, e := f.ReadDir(100)
		for _, entry := range entries {
			if entry.IsDir() || !validID(entry.Name()) {
				continue
			}
			var s Session
			e := readSealed(res, filepath.Join(keyFor(res, entry.Name()), "session.enc"), &s)
			if errors.Is(e, ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			if e = validateSession(s, entry.Name()); e != nil {
				return e
			}
			if !live(s) {
				continue
			}
			n++
			for _, c := range s.Spec.Components {
				if bytes >= limits.PendingBytes-c.Size {
					bytes = limits.PendingBytes
				} else {
					bytes += c.Size
				}
			}
			if n >= limits.Pending || bytes >= limits.PendingBytes {
				return ErrBusy
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	var incoming int64
	for _, c := range spec.Components {
		if incoming >= limits.PendingBytes-c.Size {
			incoming = limits.PendingBytes
		} else {
			incoming += c.Size
		}
	}
	// One oversized original can proceed alone; this is backlog pressure, not a
	// file-size restriction. Filesystem/quota admission still checks its full size.
	if n > 0 && incoming > limits.PendingBytes-bytes {
		return ErrBusy
	}
	return nil
}
