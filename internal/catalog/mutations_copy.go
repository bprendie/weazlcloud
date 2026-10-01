package catalog

import (
	"strings"
	"time"
)

func sameReference(a, b *Reference) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (c *Catalog) Copy(oldPath, newPath string) error {
	return c.CopyWith(oldPath, newPath, nil)
}

// CopyWith permits the storage layer to issue an independent owner reference
// before the copied catalog becomes visible.
func (c *Catalog) CopyWith(oldPath, newPath string, grant func(source *File, destination *File) error) error {
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
	for _, original := range source {
		sourceFile := cloneFile(original)
		f := cloneFile(original)
		f.Path = newPath + strings.TrimPrefix(f.Path, oldPath)
		f.EntryID, f.Revision = "", 0
		f.DeletedAt = nil
		if err := assignIdentity(&f); err != nil {
			return err
		}
		if grant != nil {
			if err := grant(&sourceFile, &f); err != nil {
				return err
			}
		}
		next = append(next, f)
	}
	oldIDs := make([]string, len(source))
	for i, original := range source {
		oldIDs[i] = original.EntryID
	}
	relinkCopiedPhotoPairs(next, len(c.files), oldIDs)
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
	selected := photoMutationSelection(c.files, path, false)
	now := time.Now().UTC()
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if selected[f.EntryID] {
			if f.Present {
				if f.Revision == ^uint64(0) {
					return ErrRevisionOverflow
				}
				next[i].Present = false
				next[i].Revision++
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
	selected := photoMutationSelection(c.files, path, true)
	var restoring []File
	for _, f := range c.files {
		if selected[f.EntryID] {
			restoring = append(restoring, f)
		}
	}
	if len(restoring) == 0 {
		return ErrNotFound
	}
	for _, candidate := range restoring {
		for _, existing := range c.files {
			if !existing.Present {
				continue
			}
			if existing.Path == candidate.Path || (!existing.Folder && strings.HasPrefix(candidate.Path, existing.Path+"/")) || (!candidate.Folder && strings.HasPrefix(existing.Path, candidate.Path+"/")) {
				return ErrConflict
			}
		}
	}
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if selected[f.EntryID] {
			if f.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			next[i].Present = true
			next[i].Revision++
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
