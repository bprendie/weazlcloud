package accountlifecycle

import (
	"context"
	"os"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

type Manager struct {
	users    *users.Store
	registry *filesvc.Registry
	capsules *capsule.Store
	uploads  *upload.Manager
}

func New(us *users.Store, registry *filesvc.Registry, caps *capsule.Store, uploads *upload.Manager) *Manager {
	return &Manager{users: us, registry: registry, capsules: caps, uploads: uploads}
}

func (m *Manager) SetDisabled(ctx context.Context, id string, disabled bool) error {
	u, ok := m.users.User(id)
	if !ok {
		return os.ErrNotExist
	}
	if err := m.users.SetDisabled(id, disabled); err != nil {
		return err
	}
	if !disabled {
		m.registry.Open(id)
		return nil
	}
	fail := func(stage string, err error) error { _ = m.users.SetDisableError(id, stage); return err }
	if err := m.registry.Block(ctx, id); err != nil {
		return fail("draining", err)
	}
	if err := m.registry.DrainResource(ctx, id); err != nil {
		return fail("draining", err)
	}
	if err := m.capsules.RevokeAll(u.ID); err != nil {
		return fail("grab links", err)
	}
	return m.users.SetDisableError(id, "")
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	u, err := m.users.BeginDelete(id)
	if err != nil {
		return err
	}
	return m.finish(ctx, u)
}

func (m *Manager) ResumePending(ctx context.Context) error {
	var first error
	for _, u := range m.users.Users() {
		if !u.Deleting {
			if u.Disabled && u.DisablePending {
				if err := m.SetDisabled(ctx, u.ID, true); err != nil && first == nil {
					first = err
				}
			}
			continue
		}
		if err := m.finish(ctx, u); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) finish(ctx context.Context, u users.User) error {
	fail := func(stage string, err error) error { _ = m.users.SetDeleteError(u.ID, stage); return err }
	if err := m.registry.Block(ctx, u.ID); err != nil {
		return fail("draining", err)
	}
	if err := m.registry.DrainResource(ctx, u.ID); err != nil {
		return fail("draining", err)
	}
	if err := m.uploads.DeleteOwner(u.ID); err != nil {
		return fail("uploads", err)
	}
	if err := m.capsules.DeleteOwner(u.ID); err != nil {
		return fail("grab links", err)
	}
	path, err := m.users.DataPath(u)
	if err != nil {
		return fail("user data", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fail("user data", err)
	}
	if err := m.users.CompleteDelete(u.ID); err != nil {
		return fail("account record", err)
	}
	return nil
}
