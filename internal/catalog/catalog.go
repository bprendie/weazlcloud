package catalog

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type File struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mtime   time.Time `json:"mtime"`
	Hash    string    `json:"hash"`
	Snap    string    `json:"snap"`
	Present bool      `json:"present"`
}

type tree struct {
	Files []File `json:"files"`
}

type Catalog struct {
	mu    sync.Mutex
	path  string
	vault *vault.Vault
	files []File
}

func New(path string, v *vault.Vault) *Catalog {
	return &Catalog{path: path, vault: v}
}

func (c *Catalog) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path)
	if os.IsNotExist(err) {
		c.files = nil
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := c.vault.Unwrap(b)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	var t tree
	if err := json.Unmarshal(plain, &t); err != nil {
		return err
	}
	c.files = t.Files
	return nil
}

func (c *Catalog) List() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0, len(c.files))
	for _, f := range c.files {
		if f.Present {
			out = append(out, f)
		}
	}
	return out
}

func (c *Catalog) Get(path string) (File, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if f.Path == path && f.Present {
			return f, true
		}
	}
	return File{}, false
}

func (c *Catalog) Put(f File) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f.Present = true
	found := false
	for i, x := range c.files {
		if x.Path == f.Path {
			c.files[i] = f
			found = true
			break
		}
	}
	if !found {
		c.files = append(c.files, f)
	}
	return c.saveLocked()
}

func (c *Catalog) Delete(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	for i, f := range c.files {
		if f.Path == path || strings.HasPrefix(f.Path, path+"/") {
			if f.Present {
				c.files[i].Present = false
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return c.saveLocked()
}

func (c *Catalog) saveLocked() error {
	plain, err := json.Marshal(tree{Files: c.files})
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	raw, err := c.vault.Wrap(plain)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(c.path, append(raw, '\n'), 0o600)
}
