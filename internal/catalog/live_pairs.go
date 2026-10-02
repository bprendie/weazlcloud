package catalog

import "strings"

// SetLivePhotoPair binds existing immutable resources without moving or rewriting
// either original. Caller also validates inherited visibility in its owner vault.
func (c *Catalog) SetLivePhotoPair(stillID, motionID string, stillRevision, motionRevision uint64, unlink bool) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := append([]File(nil), c.files...)
	a, b := -1, -1
	for i, f := range next {
		if f.EntryID == stillID && f.Present {
			a = i
		}
		if f.EntryID == motionID && f.Present {
			b = i
		}
	}
	if a < 0 || b < 0 || a == b {
		return File{}, ErrNotFound
	}
	still, motion := cloneFile(next[a]), cloneFile(next[b])
	if still.Revision != stillRevision || motion.Revision != motionRevision {
		return File{}, ErrRevisionMismatch
	}
	if still.Revision == ^uint64(0) || motion.Revision == ^uint64(0) {
		return File{}, ErrRevisionOverflow
	}
	if still.Folder || motion.Folder || still.PhotoParentID != "" {
		return File{}, ErrConflict
	}
	previousAlbums := c.albums
	c.albums = cloneAlbums(c.albums)
	defer func() { c.albums = previousAlbums }()
	if unlink {
		if motion.PhotoParentID != still.EntryID {
			return File{}, ErrConflict
		}
		motion.PhotoParentID = ""
		still.PhotoComponents = nil
		for i := range c.albums {
			album := &c.albums[i]
			for _, id := range motion.PhotoPairAlbums {
				if album.ID == id && !containsPhotoID(album.AssetIDs, motionID) {
					if album.Revision == ^uint64(0) {
						return File{}, ErrRevisionOverflow
					}
					album.AssetIDs = append(album.AssetIDs, motionID)
					album.Revision++
				}
			}
		}
		motion.PhotoPairAlbums = nil
	} else {
		if motion.PhotoParentID == stillID && len(still.PhotoComponents) == 2 {
			return cloneFile(next[a]), nil
		}
		if motion.PhotoParentID != "" || len(still.PhotoComponents) > 1 || len(motion.PhotoComponents) > 1 || still.Hidden != motion.Hidden || still.Archived != motion.Archived || still.Favorite != motion.Favorite {
			return File{}, ErrConflict
		}
		still.PhotoComponents = []PhotoComponent{{ID: "original", AssetID: stillID, MediaType: pairMediaType(still.Path)}, {ID: "motion", AssetID: motionID, MediaType: pairMediaType(motion.Path)}}
		motion.PhotoParentID = stillID
		for i := range c.albums {
			album := &c.albums[i]
			if !containsPhotoID(album.AssetIDs, motionID) {
				continue
			}
			if album.Revision == ^uint64(0) {
				return File{}, ErrRevisionOverflow
			}
			motion.PhotoPairAlbums = append(motion.PhotoPairAlbums, album.ID)
			ids := make([]string, 0, len(album.AssetIDs))
			for _, id := range album.AssetIDs {
				if id != motionID {
					ids = append(ids, id)
				}
			}
			if !containsPhotoID(ids, stillID) {
				ids = append(ids, stillID)
			}
			album.AssetIDs = ids
			album.Revision++
		}
	}
	still.Revision++
	motion.Revision++
	next[a], next[b] = still, motion
	if err := c.saveFilesLocked(next); err != nil {
		return File{}, err
	}
	c.files = next
	previousAlbums = c.albums
	return cloneFile(still), nil
}
func containsPhotoID(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}
func pairMediaType(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".heic", ".heif"} {
		if strings.HasSuffix(lower, ext) {
			return "image/heic"
		}
	}
	if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
		return "image/jpeg"
	}
	if strings.HasSuffix(lower, ".mov") {
		return "video/quicktime"
	}
	if strings.HasSuffix(lower, ".mp4") {
		return "video/mp4"
	}
	return "application/octet-stream"
}
