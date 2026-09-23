package catalog

import (
	"strings"
	"time"
)

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
	providedID, providedRevision := f.EntryID, f.Revision
	next := append([]File(nil), c.files...)
	found := false
	for i, x := range next {
		if x.Path == f.Path && x.Present {
			if x.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			if providedID != "" {
				if providedID == x.EntryID && providedRevision == x.Revision && sameReference(f.Reference, x.Reference) && f.Path == x.Path && f.Folder == x.Folder && f.Size == x.Size && f.Mtime.Equal(x.Mtime) && f.Hash == x.Hash && f.Snap == x.Snap && f.Object == x.Object {
					return nil
				}
				if providedID != x.EntryID || providedRevision != x.Revision+1 {
					return ErrRevisionMismatch
				}
				f.EntryID, f.Revision = providedID, providedRevision
			} else {
				f.EntryID = x.EntryID
				f.Revision = x.Revision + 1
			}
			if err := assignReference(&f); err != nil {
				return err
			}
			next[i] = f
			found = true
			break
		}
	}
	if !found {
		if providedID != "" {
			if providedRevision != 1 {
				return ErrRevisionMismatch
			}
			for _, old := range c.files {
				if old.EntryID == providedID {
					return ErrRevisionMismatch
				}
			}
			if err := assignReference(&f); err != nil {
				return err
			}
		} else {
			f.EntryID, f.Revision = "", 0
			if err := assignIdentity(&f); err != nil {
				return err
			}
		}
		next = append(next, f)
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

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
				if f.Revision == ^uint64(0) {
					return ErrRevisionOverflow
				}
				next[i].Present = false
				next[i].Revision++
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
		if !f.Present && f.DeletedAt != nil && (f.Path == path || strings.HasPrefix(f.Path, path+"/")) {
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
