package mobileparts

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

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
	return m.AppendGuarded(ctx, res, id, device, component, index, length, hash, src, nil)
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
