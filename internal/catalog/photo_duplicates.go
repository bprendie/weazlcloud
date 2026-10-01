package catalog

// PreferPhoto changes only owner presentation metadata, never bytes or album
// membership. The library supplies the authorized visible duplicate group.
func (c *Catalog) PreferPhoto(id string, revision uint64, ids []string) ([]File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	members := make(map[string]bool, len(ids))
	for _, id := range ids {
		members[id] = true
	}
	var target File
	for _, file := range c.files {
		if file.EntryID == id && file.Present && !file.Folder {
			target = file
			break
		}
	}
	if target.EntryID == "" || !members[id] {
		return nil, ErrNotFound
	}
	if target.Revision != revision {
		return nil, ErrRevisionMismatch
	}
	next := append([]File(nil), c.files...)
	changed := []File{}
	for i, file := range next {
		if !members[file.EntryID] {
			continue
		}
		if !file.Present || file.Folder || file.Hash == "" || file.Hash != target.Hash {
			return nil, ErrNotFound
		}
		preferred := file.EntryID == id
		if file.PreferredPhoto == preferred {
			continue
		}
		if file.Revision == ^uint64(0) {
			return nil, ErrRevisionOverflow
		}
		next[i].PreferredPhoto = preferred
		next[i].Revision++
		changed = append(changed, cloneFile(next[i]))
	}
	if len(changed) == 0 {
		return changed, nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return nil, err
	}
	c.files = next
	return changed, nil
}
