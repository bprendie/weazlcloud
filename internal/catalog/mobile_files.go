package catalog

import (
	"sort"
	"strings"
)

// MobileFile returns a detached owner-catalog entry, never a path lookup.
func (c *Catalog) MobileFile(id string) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if f.EntryID == id && f.Present && MobileFilesMatch(f.Path, "") {
			return cloneFile(f), nil
		}
	}
	return File{}, ErrNotFound
}

// MobileFilesSnapshot bounds retained rows even for a large catalog. Callers
// capture SyncPosition first and reconcile mutations by replaying its deltas.
func (c *Catalog) MobileFilesSnapshot(after, prefix string, limit int) ([]File, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit = max(1, min(limit, 200))
	rows := make([]File, 0, limit+1)
	for _, f := range c.files {
		if !f.Present || f.EntryID <= after || !MobileFilesMatch(f.Path, prefix) {
			continue
		}
		i := sort.Search(len(rows), func(i int) bool { return rows[i].EntryID > f.EntryID })
		if i > limit {
			continue
		}
		rows = append(rows, File{})
		copy(rows[i+1:], rows[i:])
		rows[i] = f
		if len(rows) > limit+1 {
			rows = rows[:limit+1]
		}
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for i := range rows {
		rows[i] = cloneFile(rows[i])
	}
	return rows, more
}

func MobileFilesMatch(name, prefix string) bool {
	// Private coordinator staging is never a native Files resource.
	if strings.HasPrefix(name, ".weazl-") {
		return false
	}
	return prefix == "" || name == prefix || strings.HasPrefix(name, prefix+"/")
}
