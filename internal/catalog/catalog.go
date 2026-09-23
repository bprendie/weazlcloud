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
	ErrConflict         = errors.New("library path conflicts with an existing file or folder")
	ErrNotFound         = errors.New("library path does not exist")
	ErrDescendant       = errors.New("cannot move a folder into itself or a descendant")
	ErrUnknownReference = errors.New("catalog contains an unsupported storage reference")
	ErrRevisionOverflow = errors.New("catalog entry revision overflow")
)

const ResticBackend = "restic"

type Reference struct {
	Backend  string `json:"backend"`
	Version  uint16 `json:"version"`
	Snapshot string `json:"snapshot"`
	Object   string `json:"object"`
}

type File struct {
	EntryID   string     `json:"entry_id,omitempty"`
	Revision  uint64     `json:"revision,omitempty"`
	Path      string     `json:"path"`
	Folder    bool       `json:"folder,omitempty"`
	Size      int64      `json:"size"`
	Mtime     time.Time  `json:"mtime"`
	Hash      string     `json:"hash"`
	Snap      string     `json:"snap"`
	Object    string     `json:"object,omitempty"`
	Reference *Reference `json:"reference,omitempty"`
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
	c.files = nil
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
	files, changed, err := upgradeFiles(t.Files)
	if err != nil {
		return err
	}
	if changed {
		if err := c.saveFilesLocked(files); err != nil {
			return err
		}
	}
	c.files = files
	return nil
}

func (c *Catalog) List() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0, len(c.files))
	for _, f := range c.files {
		if f.Present {
			out = append(out, cloneFile(f))
		}
	}
	return out
}

func (c *Catalog) All() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, len(c.files))
	for i, f := range c.files {
		out[i] = cloneFile(f)
	}
	return out
}

func (c *Catalog) Trash() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0)
	for _, f := range c.files {
		if !f.Present && f.DeletedAt != nil {
			out = append(out, cloneFile(f))
		}
	}
	return out
}

func (c *Catalog) Get(path string) (File, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if f.Path == path && f.Present {
			return cloneFile(f), true
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
		if x.Path == f.Path && x.Present {
			if x.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			f.EntryID = x.EntryID
			f.Revision = x.Revision + 1
			if err := assignReference(&f); err != nil {
				return err
			}
			next[i] = f
			found = true
			break
		}
	}
	if !found {
		f.EntryID, f.Revision = "", 0
		if err := assignIdentity(&f); err != nil {
			return err
		}
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
	f := File{Path: path, Folder: true, Mtime: time.Now().UTC(), Present: true}
	if err := assignIdentity(&f); err != nil {
		return err
	}
	next = append(next, f)
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
		if f.Revision == ^uint64(0) {
			return ErrRevisionOverflow
		}
		suffix := strings.TrimPrefix(f.Path, oldPath)
		next[i].Path = newPath + suffix
		next[i].Revision++
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
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
