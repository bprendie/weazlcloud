package catalog

// Acknowledgement changes an internal outbox flag, not the asset revision or
// its pixel identity. The durable job was already saved before this call.
func (c *Catalog) AcknowledgePhotoProcessing(versions map[string]uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := append([]File(nil), c.files...)
	changed := false
	for i := range next {
		file := &next[i]
		if file.PhotoProcessingPending && file.Present && versions[file.EntryID] == file.Revision {
			file.PhotoProcessingPending = false
			changed = true
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
