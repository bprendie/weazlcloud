package catalog

import "strings"

type PhotoBatchResult struct {
	File File
	Err  error
}

// The shadow is private to a locked transaction. Its mutations validate normally
// but never persist or become visible until the real catalog saves once.
func (c *Catalog) photoShadowLocked() *Catalog {
	return &Catalog{files: c.files, albums: c.albums, collections: c.collections, batchOnly: true}
}

func (c *Catalog) PutPhotoComponents(files []File) []PhotoBatchResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	shadow := c.photoShadowLocked()
	results := make([]PhotoBatchResult, len(files))
	changed := false
	for i, f := range files {
		if !strings.HasPrefix(f.Path, ".weazl-mobile-pending/") || f.Folder {
			results[i].Err = ErrConflict
			continue
		}
		results[i].Err = shadow.Put(f)
		if results[i].Err == nil {
			changed = true
			for _, stored := range shadow.files {
				if stored.Path == f.Path && stored.Present {
					results[i].File = cloneFile(stored)
					break
				}
			}
		}
	}
	return c.finishPhotoBatchLocked(shadow, results, changed)
}

// Invalid members remain errors; healthy members publish in one atomic write.
// The caller holds the same authorization guard for every member of this batch.
func (c *Catalog) CommitPhotoIngestBatch(commits []PhotoIngestCommit) []PhotoBatchResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	shadow := c.photoShadowLocked()
	results := make([]PhotoBatchResult, len(commits))
	changed := false
	for i, commit := range commits {
		results[i].File, results[i].Err = shadow.CommitPhotoIngest(commit)
		changed = changed || results[i].Err == nil
	}
	return c.finishPhotoBatchLocked(shadow, results, changed)
}

func (c *Catalog) finishPhotoBatchLocked(shadow *Catalog, results []PhotoBatchResult, changed bool) []PhotoBatchResult {
	if !changed {
		return results
	}
	albums, collections := c.albums, c.collections
	c.albums, c.collections = shadow.albums, shadow.collections
	if err := c.saveFilesLocked(shadow.files); err != nil {
		c.albums, c.collections = albums, collections
		for i := range results {
			if results[i].Err == nil {
				results[i] = PhotoBatchResult{Err: err}
			}
		}
		return results
	}
	c.files = shadow.files
	return results
}
