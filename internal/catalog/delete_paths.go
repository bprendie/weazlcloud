package catalog

import "time"

func (c *Catalog) DeletePaths(paths []string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	selected := make(map[string]bool)
	requested := make(map[string]bool, len(paths))
	for _, path := range paths {
		requested[path] = true
	}
	for _, file := range c.files {
		if file.Present && requested[file.Path] {
			selected[file.EntryID] = true
			for _, part := range file.PhotoComponents {
				if part.ID != "original" {
					selected[part.AssetID] = true
				}
			}
		}
	}
	next := append([]File(nil), c.files...)
	now := time.Now().UTC()
	affected := []string{}
	for i := range next {
		file := &next[i]
		if !selected[file.EntryID] || !file.Present {
			continue
		}
		if file.Revision == ^uint64(0) {
			return nil, ErrRevisionOverflow
		}
		file.Present = false
		file.Revision++
		file.DeletedAt = &now
		affected = append(affected, file.Path)
	}
	if len(affected) == 0 {
		return affected, nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return nil, err
	}
	c.files = next
	return affected, nil
}
