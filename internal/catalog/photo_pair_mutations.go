package catalog

import "strings"

// RelatedPhotoPaths returns the exact members affected by a normal path
// deletion/restore, including components deleted with their primary asset.
func (c *Catalog) RelatedPhotoPaths(path string, restore bool) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	selected := photoMutationSelection(c.files, path, restore)
	paths := []string{}
	for _, file := range c.files {
		if selected[file.EntryID] {
			paths = append(paths, file.Path)
		}
	}
	return paths
}

func photoMutationSelection(files []File, path string, restore bool) map[string]bool {
	selected := make(map[string]bool)
	byID := make(map[string]File, len(files))
	for _, file := range files {
		byID[file.EntryID] = file
		eligible := file.Present
		if restore {
			eligible = !file.Present && file.DeletedAt != nil
		}
		if eligible && (file.Path == path || strings.HasPrefix(file.Path, path+"/")) {
			selected[file.EntryID] = true
		}
	}
	for _, file := range files {
		if !selected[file.EntryID] {
			continue
		}
		for _, part := range file.PhotoComponents {
			component, ok := byID[part.AssetID]
			if !ok || component.PhotoParentID != file.EntryID {
				continue
			}
			if !restore && component.Present {
				selected[component.EntryID] = true
			}
			if restore && !component.Present && component.DeletedAt != nil && file.DeletedAt != nil && component.DeletedAt.Equal(*file.DeletedAt) {
				selected[component.EntryID] = true
			}
		}
	}
	return selected
}

func relinkCopiedPhotoPairs(files []File, copiedStart int, oldIDs []string) {
	mapped := make(map[string]string, len(oldIDs))
	for i, id := range oldIDs {
		mapped[id] = files[copiedStart+i].EntryID
	}
	for i := copiedStart; i < len(files); i++ {
		file := &files[i]
		file.DeviceID, file.DeviceAssetID, file.SourceRevision = "", "", ""
		file.PhotoPairAlbums = nil
		file.PhotoParentID = mapped[file.PhotoParentID]
		parts := []PhotoComponent{}
		for _, part := range file.PhotoComponents {
			if id := mapped[part.AssetID]; id != "" {
				part.AssetID = id
				parts = append(parts, part)
			}
		}
		file.PhotoComponents = parts
	}
}
