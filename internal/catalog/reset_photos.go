package catalog

import "strings"

// PhotoResetPath includes the Photos collection and its unpublished mobile
// components. Files elsewhere in the Library remain outside the reset.
func PhotoResetPath(name string) bool {
	return syncPhotoPath(name) || name == ".weazl-mobile-pending" || strings.HasPrefix(name, ".weazl-mobile-pending/")
}

// ResetPhotos is an offline maintenance operation. Callers must stop all owner
// writers, retain a rollback copy, and retire only unreferenced storage objects.
// A new sync epoch forces devices to discard their old Photos checkpoint.
func (c *Catalog) ResetPhotos() ([]File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var removed, kept []File
	removedIDs := map[string]bool{}
	for _, f := range c.files {
		if PhotoResetPath(f.Path) {
			removed = append(removed, cloneFile(f))
			removedIDs[f.EntryID] = true
		} else {
			kept = append(kept, f)
		}
	}
	// A pair moved partly into Drive needs an explicit unlink first. Do not
	// silently rewrite metadata outside the requested collection.
	for _, f := range kept {
		if removedIDs[f.PhotoParentID] {
			return nil, ErrConflict
		}
		for _, part := range f.PhotoComponents {
			if removedIDs[part.AssetID] {
				return nil, ErrConflict
			}
		}
	}
	oldFiles, oldAlbums, oldSavedAlbums := c.files, c.albums, c.savedAlbums
	oldCollections, oldSavedCollections := c.collections, c.savedCollections
	oldJournal, oldCheckpoints := c.journal, c.checkpoints
	// Start the new epoch from the retained files. Old source identifiers,
	// receipts and deleted Photos must not survive in the new change journal.
	c.files, c.albums, c.savedAlbums = kept, nil, nil
	c.collections, c.savedCollections = collectionState{}, collectionState{}
	c.journal, c.checkpoints = Journal{}, nil
	if err := c.saveFilesLocked(kept); err != nil {
		c.files, c.albums, c.savedAlbums = oldFiles, oldAlbums, oldSavedAlbums
		c.collections, c.savedCollections = oldCollections, oldSavedCollections
		c.journal, c.checkpoints = oldJournal, oldCheckpoints
		return nil, err
	}
	return removed, nil
}
