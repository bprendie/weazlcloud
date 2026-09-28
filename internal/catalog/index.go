package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
)

// Clear drops the in-memory plaintext catalog when its vault session ends.
func (c *Catalog) Clear() {
	c.mu.Lock()
	c.files, c.children, c.byPath = nil, nil, nil
	c.summaryReady = false
	c.version++
	c.mu.Unlock()
}

// Version changes after every successful load, commit, or clear.
func (c *Catalog) Version() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// DiskUnchanged cheaply detects replacement or modification by an external writer.
func (c *Catalog) DiskUnchanged() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := os.Stat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c.diskInfo == nil
	}
	if err != nil || c.diskInfo == nil {
		return false
	}
	return os.SameFile(c.diskInfo, current) && c.diskInfo.Size() == current.Size() && c.diskInfo.ModTime().Equal(current.ModTime())
}

// Summary calculates display aggregates under one catalog read lock.
func (c *Catalog) Summary() (logical, unique, trash int64, trashCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.summaryReady {
		return c.summary.logical, c.summary.unique, c.summary.trash, c.summary.trashCount
	}
	hashes := make(map[string]struct{})
	for _, f := range c.files {
		if f.Present && !f.Folder {
			logical += f.Size
			if _, exists := hashes[f.Hash]; !exists {
				hashes[f.Hash] = struct{}{}
				unique += f.Size
			}
		} else if !f.Present && f.DeletedAt != nil && !f.Folder {
			trash += f.Size
			trashCount++
		}
	}
	c.summary = catalogSummary{logical: logical, unique: unique, trash: trash, trashCount: trashCount}
	c.summaryReady = true
	return
}

func (c *Catalog) Get(name string) (File, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.byPath[name]
	return cloneFile(f), ok
}

func (c *Catalog) IsFolder(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if file, ok := c.byPath[name]; ok {
		return file.Folder
	}
	parent := name
	if i := strings.LastIndexByte(parent, '/'); i >= 0 {
		parent = parent[:i]
	} else {
		parent = ""
	}
	for _, child := range c.children[parent] {
		if child.Path == name {
			return child.Folder
		}
	}
	return false
}

// Children returns only direct children, synthesizing folders for legacy paths.
func (c *Catalog) Children(name string) []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := c.children[name]
	out := make([]File, len(rows))
	for i := range rows {
		out[i] = cloneFile(rows[i])
	}
	return out
}

// Matching returns cloned live rows accepted by an indexed query pass.
func (c *Catalog) Matching(ctx context.Context, include func(File) bool) ([]File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := make([]File, 0)
	for i, file := range c.files {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if file.Present && include(file) {
			rows = append(rows, cloneFile(file))
		}
	}
	return rows, nil
}
