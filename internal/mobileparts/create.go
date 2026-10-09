package mobileparts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
)

func (m *Manager) Create(res *filesvc.Resource, owner, id string, spec Spec) (View, error) {
	if !validID(owner) || !validID(id) {
		return View{}, ErrInvalid
	}
	spec.Components = append([]Component(nil), spec.Components...)
	spec.Payload = append(json.RawMessage(nil), spec.Payload...)
	if e := spec.normalize(); e != nil {
		return View{}, e
	}
	unlock := m.lock(res, id)
	defer unlock()
	old, e := m.load(res, id)
	if e == nil || errors.Is(e, ErrExpired) {
		original := old.Spec
		original.Components = append([]Component(nil), original.Components...)
		for i := range original.Components {
			original.Components[i].ReceivedBytes = 0
			original.Components[i].ReceivedParts = 0
		}
		a, _ := json.Marshal(original)
		b, _ := json.Marshal(spec)
		if old.OwnerID != owner || !bytes.Equal(a, b) {
			return View{}, ErrConflict
		}
		if !errors.Is(e, ErrExpired) {
			return view(old), nil
		}
		if m.isActive(res, id) {
			return View{}, ErrConflict
		}
		if e = os.RemoveAll(keyFor(res, id)); e != nil {
			return View{}, e
		}
		m.release(res, id)
	}
	if e != nil && !errors.Is(e, ErrNotFound) {
		return View{}, e
	}
	m.admissionMu.Lock()
	defer m.admissionMu.Unlock()
	if e = m.admitNew(res, spec); e != nil {
		return View{}, e
	}
	key, e := cryptox.Random(32)
	if e != nil {
		return View{}, e
	}
	defer clear(key)
	s := Session{Version: 1, ID: id, OwnerID: owner, Spec: spec, Key: cryptox.B64(key), Status: "uploading", CreatedAt: m.now().UTC()}
	if ready(s) && spec.CommitWhenComplete {
		s.Status = "queued"
	}
	if e = os.MkdirAll(keyFor(res, id), 0700); e != nil {
		return View{}, e
	}
	if e = m.reserve(res, s); e != nil {
		return View{}, e
	}
	if e = m.save(res, &s); e != nil {
		m.release(res, id)
		return View{}, e
	}
	return view(s), nil
}
