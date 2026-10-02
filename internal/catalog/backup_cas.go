package catalog

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
)

// BackupMutation is a journaled, immutable plan. Guards include the destination
// and every existing parent; absence guards prevent unrelated path overwrite.
type BackupMutation struct {
	Guards []File   `json:"guards"`
	Absent []string `json:"absent"`
	Files  []File   `json:"files"`
}

func (c *Catalog) CheckBackup(plan BackupMutation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkBackupLocked(plan)
}

func (c *Catalog) checkBackupLocked(plan BackupMutation) error {
	for _, guard := range plan.Guards {
		found := false
		for _, f := range c.files {
			if f.EntryID == guard.EntryID && f.Present && f.Revision == guard.Revision && f.Path == guard.Path && f.Folder == guard.Folder {
				found = true
				break
			}
		}
		if !found {
			return ErrRevisionMismatch
		}
	}
	for _, p := range plan.Absent {
		for _, f := range c.files {
			if f.Present && f.Path == p {
				return ErrConflict
			}
		}
	}
	return nil
}

// PublishBackup checks preconditions and publishes the entire folder/file plan
// in one encrypted catalog save. It never calls path-based Put or Rename.
func (c *Catalog) PublishBackup(plan BackupMutation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkBackupLocked(plan); err != nil {
		return err
	}
	next := append([]File(nil), c.files...)
	guarded := make(map[string]File)
	absent := make(map[string]bool)
	for _, f := range plan.Guards {
		guarded[f.EntryID] = f
	}
	for _, p := range plan.Absent {
		absent[p] = true
	}
	for _, f := range plan.Files {
		if f.EntryID == "" || !f.Present || f.DeletedAt != nil || f.Revision == 0 {
			return ErrRevisionMismatch
		}
		if err := validateReference(f); err != nil {
			return err
		}
		index := -1
		for i, old := range next {
			if old.EntryID == f.EntryID {
				index = i
				break
			}
		}
		if index >= 0 {
			old, ok := guarded[f.EntryID]
			if !ok || old.Revision == ^uint64(0) || f.Revision != old.Revision+1 || f.Folder != old.Folder {
				return ErrRevisionMismatch
			}
			next[index] = cloneFile(f)
		} else {
			if f.Revision != 1 || !absent[f.Path] {
				return ErrRevisionMismatch
			}
			next = append(next, cloneFile(f))
		}
	}
	seen := make(map[string]File)
	for _, f := range next {
		if !f.Present {
			continue
		}
		if _, exists := seen[f.Path]; exists {
			return ErrConflict
		}
		seen[f.Path] = f
	}
	for _, f := range plan.Files {
		for p := path.Dir(f.Path); p != "."; p = path.Dir(p) {
			parent, exists := seen[p]
			if !exists || !parent.Folder {
				return ErrConflict
			}
		}
	}
	// Folder moves must explicitly include every descendant, so none are left
	// beneath the old path or silently moved without their own revision checks.
	for _, old := range plan.Guards {
		if !old.Folder {
			continue
		}
		for _, f := range plan.Files {
			if f.EntryID != old.EntryID || f.Path == old.Path {
				continue
			}
			for _, child := range c.files {
				if child.Present && strings.HasPrefix(child.Path, old.Path+"/") {
					if _, ok := guarded[child.EntryID]; !ok {
						return ErrRevisionMismatch
					}
				}
			}
		}
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	dir, err := os.Open(filepath.Dir(c.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// BackupPublished proves a publication even after a later edit/trash/restore,
// using the catalog journal written atomically with the original mutation.
// If retained proof has expired, callers fail closed rather than replay a CAS.
func (c *Catalog) BackupPublished(files []File) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(files) == 0 {
		return false
	}
	for _, wanted := range files {
		found := false
		for _, f := range c.files {
			if reflect.DeepEqual(f, wanted) {
				found = true
				break
			}
		}
		if !found {
			for _, record := range c.journal.Records {
				if record.File != nil && reflect.DeepEqual(*record.File, wanted) {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// NewBackupIdentity creates a new ID independently of any path occupant.
func NewBackupIdentity() (string, error) { return newEntryID() }
