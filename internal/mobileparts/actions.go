package mobileparts

import (
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"time"
)

func (m *Manager) Retry(res *filesvc.Resource, id, device string) (View, error) {
	u := m.lock(res, id)
	defer u()
	s, e := m.load(res, id)
	if e != nil {
		return View{}, e
	}
	if s.Spec.DeviceID != device {
		return View{}, ErrNotFound
	}
	if !ready(s) || s.Status == "stored" || s.Status == "cancelled" || m.isActive(res, id) {
		return View{}, ErrConflict
	}
	s.Status = "queued"
	s.ErrorCode = ""
	s.RetryAttempts, s.RetryAfter = 0, time.Time{}
	if e = m.save(res, &s); e != nil {
		return View{}, e
	}
	return view(s), nil
}
func (m *Manager) Cancel(res *filesvc.Resource, id, device string) error {
	_, err := m.CancelCoordinated(res, id, device, nil)
	return err
}
