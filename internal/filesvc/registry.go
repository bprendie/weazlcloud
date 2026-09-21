package filesvc

import (
	"sync"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Resource is the single per-user file service shared by every protocol.
// Keeping the vault, catalog, and library together prevents desk and WebDAV
// from loading and saving competing snapshots of the same catalog.
type Resource struct {
	Vault *vault.Vault
	Lib   *library.Library
}

type Registry struct {
	users *users.Store
	mu    sync.Mutex
	items map[string]*Resource
}

func NewRegistry(us *users.Store) *Registry {
	return &Registry{users: us, items: make(map[string]*Resource)}
}

func (r *Registry) For(u users.User) *Resource {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[u.ID]; item != nil {
		return item
	}
	v := vault.New(r.users.VaultPath(u), r.users.NodeKeyPath(u))
	l := library.New(r.users.LibraryPath(u), r.users.CatalogPath(u), v)
	item := &Resource{Vault: v, Lib: l}
	r.items[u.ID] = item
	return item
}
