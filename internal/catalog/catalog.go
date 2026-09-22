package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var (
	ErrConflict   = errors.New("library path conflicts with an existing file or folder")
	ErrNotFound   = errors.New("library path does not exist")
	ErrDescendant = errors.New("cannot move a folder into itself or a descendant")
)

type File struct {
	Path      string     `json:"path"`
	Folder    bool       `json:"folder,omitempty"`
	Size      int64      `json:"size"`
	Mtime     time.Time  `json:"mtime"`
	Hash      string     `json:"hash"`
	Snap      string     `json:"snap"`
	Object    string     `json:"object,omitempty"`
	Present   bool       `json:"present"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
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

func (c *Catalog) All() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]File(nil), c.files...)
}

func (c *Catalog) Trash() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0)
	for _, f := range c.files {
		if !f.Present && f.DeletedAt != nil {
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
	for _, x := range c.files {
		if !x.Present {
			continue
		}
		if x.Path == f.Path && x.Folder != f.Folder {
			return ErrConflict
		}
		if x.Path != f.Path && !x.Folder && strings.HasPrefix(f.Path, x.Path+"/") {
			return ErrConflict
		}
		if f.Folder && x.Path != f.Path && strings.HasPrefix(x.Path, f.Path+"/") {
			return ErrConflict
		}
	}
	next := append([]File(nil), c.files...)
	found := false
	for i, x := range next {
		if x.Path == f.Path {
			next[i] = f
			found = true
			break
		}
	}
	if !found {
		next = append(next, f)
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Mkdir(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if !f.Present {
			continue
		}
		if f.Path == path || (!f.Folder && strings.HasPrefix(path, f.Path+"/")) {
			return ErrConflict
		}
	}
	next := append([]File(nil), c.files...)
	next = append(next, File{Path: path, Folder: true, Mtime: time.Now().UTC(), Present: true})
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Rename(oldPath, newPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if oldPath == newPath {
		return nil
	}
	if strings.HasPrefix(newPath, oldPath+"/") {
		return ErrDescendant
	}
	found := false
	for _, f := range c.files {
		if f.Present && (f.Path == oldPath || strings.HasPrefix(f.Path, oldPath+"/")) {
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	for _, f := range c.files {
		if !f.Present || f.Path == oldPath || strings.HasPrefix(f.Path, oldPath+"/") {
			continue
		}
		if f.Path == newPath || strings.HasPrefix(f.Path, newPath+"/") || strings.HasPrefix(newPath, f.Path+"/") {
			return ErrConflict
		}
	}
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if !f.Present || (f.Path != oldPath && !strings.HasPrefix(f.Path, oldPath+"/")) {
			continue
		}
		suffix := strings.TrimPrefix(f.Path, oldPath)
		next[i].Path = newPath + suffix
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Copy(oldPath, newPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if oldPath == newPath || strings.HasPrefix(newPath, oldPath+"/") {
		return ErrDescendant
	}
	var source []File
	for _, f := range c.files {
		if f.Present && (f.Path == oldPath || strings.HasPrefix(f.Path, oldPath+"/")) {
			source = append(source, f)
		}
	}
	if len(source) == 0 {
		return ErrNotFound
	}
	for _, f := range source {
		suffix := strings.TrimPrefix(f.Path, oldPath)
		target := newPath + suffix
		for _, existing := range c.files {
			if !existing.Present || (existing.Path == oldPath || strings.HasPrefix(existing.Path, oldPath+"/")) {
				continue
			}
			if existing.Path == target || (!existing.Folder && strings.HasPrefix(target, existing.Path+"/")) || (f.Folder && strings.HasPrefix(existing.Path, target+"/")) {
				return ErrConflict
			}
		}
	}
	next := append([]File(nil), c.files...)
	for _, f := range source {
		f.Path = newPath + strings.TrimPrefix(f.Path, oldPath)
		f.DeletedAt = nil
		next = append(next, f)
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Delete(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if f.Path == path || strings.HasPrefix(f.Path, path+"/") {
			if f.Present {
				next[i].Present = false
				now := time.Now().UTC()
				next[i].DeletedAt = &now
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Restore(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var restoring []File
	for _, f := range c.files {
		if !f.Present && f.DeletedAt != nil && (f.Path == path || strings.HasPrefix(f.Path, path+"/")) {
			restoring = append(restoring, f)
		}
	}
	if len(restoring) == 0 {
		return ErrNotFound
	}
	for _, candidate := range restoring {
		for _, existing := range c.files {
			if !existing.Present || existing.Path == candidate.Path {
				continue
			}
			if existing.Path == candidate.Path || (!existing.Folder && strings.HasPrefix(candidate.Path, existing.Path+"/")) || (candidate.Folder && strings.HasPrefix(existing.Path, candidate.Path+"/")) {
				return ErrConflict
			}
		}
	}
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if !f.Present && f.DeletedAt != nil && (f.Path == path || strings.HasPrefix(f.Path, path+"/")) {
			next[i].Present = true
			next[i].DeletedAt = nil
		}
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) PurgeTrash(before time.Time) ([]File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := make([]File, 0)
	next := make([]File, 0, len(c.files))
	for _, f := range c.files {
		if !f.Present && f.DeletedAt != nil && !f.DeletedAt.After(before) {
			removed = append(removed, f)
			continue
		}
		next = append(next, f)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return nil, err
	}
	c.files = next
	return removed, nil
}

func (c *Catalog) saveLocked() error {
	return c.saveFilesLocked(c.files)
}

func (c *Catalog) saveFilesLocked(files []File) error {
	plain, err := json.Marshal(tree{Files: files})
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
