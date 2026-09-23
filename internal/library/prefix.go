package library

import (
	"context"
	"errors"
)

const prefixLimit = 512

// Prefix inspects a bounded prefix while consuming the restic stream. The
// writer never retains more than prefixLimit bytes, but restic is still read
// to completion so its stdout pipe cannot stall on a large source file.
func (l *Library) Prefix(ctx context.Context, name string) ([]byte, error) {
	name, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	f, ok := l.catalog.Get(name)
	if !ok {
		return nil, errors.New("file is not in the library")
	}
	ref, err := l.capture(f)
	if err != nil {
		return nil, err
	}
	var prefix prefixWriter
	err = l.readReferenceRange(ctx, ref, 0, min(int64(prefixLimit), f.Size), &prefix)
	return append([]byte(nil), prefix.buf...), err
}

type prefixWriter struct {
	buf []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	if len(w.buf) < prefixLimit {
		n := prefixLimit - len(w.buf)
		if n > len(p) {
			n = len(p)
		}
		w.buf = append(w.buf, p[:n]...)
	}
	return len(p), nil
}
