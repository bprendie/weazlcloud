package catalog

// Original-source mappings publish with the pair and outbox, never a filesystem
// identifier. Moving to a new source revision preserves the previous original.
func (c *Catalog) mapPhotoSourceLocked(commit PhotoIngestCommit, file File) error {
	if commit.SourceNamespace == "" && commit.SourceAssetID == "" {
		return nil
	}
	if commit.SourceNamespace == "" || commit.SourceAssetID == "" || len(commit.SourceNamespace) > 200 || len(commit.SourceAssetID) > 4096 || SafeSourceKey(commit.SourceNamespace, commit.SourceAssetID) != commit.DeviceAssetID {
		return ErrConflict
	}
	key := sourceKey(commit.DeviceID, commit.SourceNamespace, commit.SourceAssetID)
	old, exists := c.collections.Sources[key]
	if exists {
		if old.Kind != "asset" || old.ServerRevision != commit.SourceMappingRevision {
			return ErrRevisionMismatch
		}
		current := false
		for _, f := range c.files {
			if f.EntryID == old.ServerID && f.Present && f.Revision == old.ServerRevision {
				current = true
				break
			}
		}
		if !current {
			return ErrRevisionMismatch
		}
	} else if commit.SourceMappingRevision != 0 {
		return ErrRevisionMismatch
	}
	c.collections = cloneCollectionState(c.collections)
	c.collections.Sources[key] = SourceMapping{DeviceID: commit.DeviceID, Namespace: commit.SourceNamespace, SourceID: commit.SourceAssetID, SourceRevision: commit.SourceRevision, Kind: "asset", ServerID: file.EntryID, ServerRevision: file.Revision}
	return nil
}
