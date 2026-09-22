package filesvc

import (
	"context"
	"sync"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Resource is the single per-user file service shared by every protocol.
// Keeping the vault, catalog, and library together prevents desk and WebDAV
// from loading and saving competing snapshots of the same catalog.
type Resource struct {
	Vault    *vault.Vault
	Lib      *library.Library
	Changes  *Hub
	Archives *ArchiveManager
}

type Registry struct {
	users    *users.Store
	quota    *quota.Manager
	activity func() func()
	mu       sync.Mutex
	items    map[string]*Resource
	gates    map[string]*userGate
}

func NewRegistry(us *users.Store, q ...*quota.Manager) *Registry {
	var manager *quota.Manager
	if len(q) > 0 {
		manager = q[0]
	}
	return &Registry{users: us, quota: manager, items: make(map[string]*Resource), gates: make(map[string]*userGate)}
}

func (r *Registry) SetActivityTracker(track func() func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activity = track
	for _, resource := range r.items {
		resource.Lib.SetActivityTracker(track)
		resource.Archives.SetActivityTracker(track)
	}
}

func (r *Registry) For(u users.User) *Resource {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[u.ID]; item != nil {
		return item
	}
	v := vault.New(r.users.VaultPath(u), r.users.NodeKeyPath(u))
	l := library.New(r.users.LibraryPath(u), r.users.CatalogPath(u), v)
	changes := NewHub()
	l.SetChangeSink(changes)
	var reserve func(int64) (func(), error)
	if r.quota != nil {
		reserve = func(bytes int64) (func(), error) {
			used, err := l.Usage(context.Background())
			if err != nil {
				return nil, err
			}
			return r.quota.Reserve(u.ID, r.users.Count(), used, 0, bytes)
		}
	}
	item := &Resource{Vault: v, Lib: l, Changes: changes, Archives: NewArchiveManager(l, reserve)}
	item.Lib.SetActivityTracker(r.activity)
	item.Archives.SetActivityTracker(r.activity)
	r.items[u.ID] = item
	return item
}
