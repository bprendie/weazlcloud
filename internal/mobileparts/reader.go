package mobileparts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func partKey(s Session, component string, index int64) ([]byte, error) {
	key, e := cryptox.B64d(s.Key)
	if e != nil || len(key) != 32 {
		return nil, ErrCorrupt
	}
	defer clear(key)
	h := sha256.New()
	h.Write(key)
	fmt.Fprintf(h, "\x00%s\x00%d", component, index)
	return h.Sum(nil), nil
}

type partsReader struct {
	ctx    context.Context
	res    *filesvc.Resource
	s      Session
	c      Component
	index  int64
	reader *cryptox.StreamFileReader
	closed bool
}

func (m *Manager) Open(ctx context.Context, res *filesvc.Resource, id, component string) (io.ReadCloser, error) {
	u := m.lock(res, id)
	defer u()
	s, e := m.load(res, id)
	if e != nil {
		return nil, e
	}
	if s.Status == "stored" || s.Status == "cancelled" {
		return nil, ErrConflict
	}
	if !ready(s) {
		return nil, ErrIncomplete
	}
	for _, c := range s.Spec.Components {
		if c.ID == component {
			return &partsReader{ctx: ctx, res: res, s: s, c: c}, nil
		}
	}
	return nil, ErrInvalid
}
func (r *partsReader) Read(b []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if !r.res.Vault.Unlocked() {
		return 0, vault.ErrLocked
	}
	for r.index < count(r.c) {
		if r.reader == nil {
			key, e := partKey(r.s, r.c.ID, r.index)
			if e != nil {
				return 0, e
			}
			size, _ := partLength(r.c, r.index)
			r.reader, e = cryptox.OpenStreamFile(r.ctx, filepath.Join(keyFor(r.res, r.s.ID), partName(r.c.ID, r.index)+".wza"), key, size)
			clear(key)
			if e != nil {
				return 0, e
			}
		}
		n, e := r.reader.Read(b)
		if e == io.EOF {
			r.reader.Close()
			r.reader = nil
			r.index++
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, e
	}
	return 0, io.EOF
}
func (r *partsReader) Close() error {
	r.closed = true
	clear([]byte(r.s.Key))
	r.s.Key = ""
	if r.reader != nil {
		return r.reader.Close()
	}
	return nil
}
func (m *Manager) Verify(ctx context.Context, res *filesvc.Resource, id string) error {
	u := m.lock(res, id)
	s, e := m.load(res, id)
	u()
	if e != nil {
		return e
	}
	if !ready(s) {
		return ErrIncomplete
	}
	for _, c := range s.Spec.Components {
		r, e := m.Open(ctx, res, id, c.ID)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, r)
		r.Close()
		if e != nil {
			return e
		}
		if n != c.Size || hex.EncodeToString(h.Sum(nil)) != c.SHA256 {
			return ErrChecksum
		}
	}
	return nil
}
