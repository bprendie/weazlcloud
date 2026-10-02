package catalog

import (
	"errors"
	"strings"
	"time"
)

// SwitchReference changes one exact catalog version after its replacement has
// been copied and verified. It preserves path, metadata, and Trash timestamps.
func (c *Catalog) SwitchReference(entryID string, revision uint64, previous, next Reference) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	files := append([]File(nil), c.files...)
	for i := range files {
		f := &files[i]
		if f.EntryID != entryID || f.Revision != revision {
			continue
		}
		if f.Folder || f.Reference == nil || *f.Reference != previous || f.Revision == ^uint64(0) {
			return File{}, ErrRevisionMismatch
		}
		if next.Backend != SharedBackend || next.Version == 0 || next.Object == "" || next.Operation == "" || next.OwnerEntryID != entryID || next.OwnerRevision != revision+1 || next.Snapshot != "" {
			return File{}, ErrUnknownReference
		}
		f.Revision++
		f.Reference = &next
		f.Snap = ""
		f.Object = next.Object
		if err := validateReference(*f); err != nil {
			return File{}, err
		}
		if err := c.saveFilesLocked(files); err != nil {
			return File{}, err
		}
		c.files = files
		return cloneFile(*f), nil
	}
	return File{}, errors.New("catalog version changed before migration switch")
}

func (c *Catalog) Put(f File) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f.Hidden && !f.Folder {
		return ErrInvalidHiddenEntry
	}
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
			f.Hidden = x.Hidden
			if f.ImportedAt.IsZero() {
				f.ImportedAt = x.ImportedAt
				if f.ImportedAt.IsZero() {
					f.ImportedAt = x.Mtime
				}
			}
			if x.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			if providedID != "" {
				if providedID == x.EntryID && providedRevision == x.Revision && sameReference(f.Reference, x.Reference) && f.Path == x.Path && f.Folder == x.Folder && f.Size == x.Size && f.Mtime.Equal(x.Mtime) && f.ImportedAt.Equal(x.ImportedAt) && sameCaptureFile(f, x) && f.Hash == x.Hash && f.Snap == x.Snap && f.Object == x.Object {
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
		if f.ImportedAt.IsZero() {
			f.ImportedAt = time.Now().UTC()
		}
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
