package mobileparts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
)

func (m *Manager) Status(res *filesvc.Resource, id, device string) (View, error) {
	u := m.lock(res, id)
	defer u()
	s, e := m.load(res, id)
	if e != nil {
		return View{}, e
	}
	if s.Spec.DeviceID != device {
		return View{}, ErrNotFound
	}
	return view(s), nil
}
func (m *Manager) Append(ctx context.Context, res *filesvc.Resource, id, device, component string, index, length int64, hash string, src io.Reader) (View, error) {
	u := m.lock(res, id)
	defer u()
	s, e := m.load(res, id)
	if e != nil {
		return View{}, e
	}
	if s.Spec.DeviceID != device {
		return View{}, ErrNotFound
	}
	var c *Component
	for i := range s.Spec.Components {
		if s.Spec.Components[i].ID == component {
			c = &s.Spec.Components[i]
		}
	}
	if c == nil {
		return View{}, ErrInvalid
	}
	expected, e := partLength(*c, index)
	if e != nil || length != expected || !validHash(hash) {
		return View{}, ErrInvalid
	}
	hash = strings.ToLower(hash)
	base := filepath.Join(keyFor(res, id), partName(component, index))
	var old partReceipt
	if e = readSealed(res, base+".receipt.enc", &old); e == nil {
		if old.Hash != hash || old.Size != expected {
			return View{}, ErrConflict
		}
		h := sha256.New()
		n, readErr := io.Copy(h, io.LimitReader(contextReader{ctx, src}, expected+1))
		if readErr != nil {
			return View{}, readErr
		}
		if n != expected {
			return View{}, ErrInvalid
		}
		if hex.EncodeToString(h.Sum(nil)) != hash {
			return View{}, ErrChecksum
		}
		return view(s), nil
	} else if !errors.Is(e, ErrNotFound) {
		return View{}, e
	}
	if s.Status != "uploading" {
		return View{}, ErrConflict
	}
	key, e := partKey(s, component, index)
	if e != nil {
		return View{}, e
	}
	defer clear(key)
	f, e := os.CreateTemp(keyFor(res, id), ".incoming-")
	if e != nil {
		return View{}, e
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return View{}, e
	}
	w, e := cryptox.NewStreamFileWriter(f, key)
	if e != nil {
		return View{}, e
	}
	h := sha256.New()
	n, e := io.Copy(w, io.TeeReader(io.LimitReader(contextReader{ctx, src}, expected+1), h))
	closeErr := w.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return View{}, e
	}
	if n != expected {
		return View{}, ErrInvalid
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return View{}, ErrChecksum
	}
	if e = f.Sync(); e != nil {
		return View{}, e
	}
	if e = f.Close(); e != nil {
		return View{}, e
	}
	if e = os.Rename(name, base+".wza"); e != nil {
		return View{}, e
	}
	if e = syncDir(keyFor(res, id)); e != nil {
		return View{}, e
	}
	if e = writeSealed(res, base+".receipt.enc", partReceipt{component, index, n, hash}); e != nil {
		return View{}, e
	}
	c.ReceivedBytes += n
	c.ReceivedParts++
	if ready(s) && s.Spec.CommitWhenComplete {
		s.Status = "queued"
	}
	if e = m.save(res, &s); e != nil {
		return View{}, e
	}
	return view(s), nil
}
func (m *Manager) Missing(res *filesvc.Resource, id, device, component string, start int64, limit int) (PartPage, error) {
	u := m.lock(res, id)
	defer u()
	s, e := m.load(res, id)
	if e != nil {
		return PartPage{}, e
	}
	if s.Spec.DeviceID != device {
		return PartPage{}, ErrNotFound
	}
	var c *Component
	for i := range s.Spec.Components {
		if s.Spec.Components[i].ID == component {
			c = &s.Spec.Components[i]
		}
	}
	if c == nil || start < 0 || start > count(*c) {
		return PartPage{}, ErrInvalid
	}
	p := PartPage{Missing: []int64{}}
	limit = max(1, min(limit, 200))
	end := min(count(*c), start+200)
	for i := start; i < end; i++ {
		_, e = os.Stat(filepath.Join(keyFor(res, id), partName(component, i)+".receipt.enc"))
		if errors.Is(e, os.ErrNotExist) {
			p.Missing = append(p.Missing, i)
		} else if e != nil {
			return p, e
		}
		p.Next = i + 1
		if len(p.Missing) >= limit {
			break
		}
	}
	p.HasMore = p.Next < count(*c)
	return p, nil
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.src.Read(b)
}
func syncDir(name string) error {
	f, e := os.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
