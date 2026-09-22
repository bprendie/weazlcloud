package catalog

import (
	"strings"
	"time"
)

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
