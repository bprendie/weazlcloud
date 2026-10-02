package catalog

import (
	"path"
	"strings"
)

type PhotoComponent struct {
	ID        string `json:"id"`
	AssetID   string `json:"asset_id"`
	MediaType string `json:"media_type"`
}

type PhotoIngestFile struct {
	ID, From, To, Hash, MediaType string
	Size                          int64
}

type PhotoIngestCommit struct {
	SourceNamespace, SourceAssetID          string
	SourceMappingRevision                   uint64
	DeviceID, DeviceAssetID, SourceRevision string
	Hidden                                  bool
	OpaqueOriginal                          bool
	Files                                   []PhotoIngestFile
	AlbumIDs                                []string
	Capture                                 *CaptureMetadata
}

// Components and album membership publish in one encrypted catalog write.
// Retries identify immutable uploaded content and never overwrite another file.
func (c *Catalog) CommitPhotoIngest(commit PhotoIngestCommit) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(commit.Files) < 1 || len(commit.Files) > 2 || commit.Files[0].ID != "original" {
		return File{}, ErrConflict
	}
	next := append([]File(nil), c.files...)
	alreadyCommitted := true
	indices := make([]int, len(commit.Files))
	components := make([]PhotoComponent, len(commit.Files))
	seenParts, seenFrom, seenTo := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, part := range commit.Files {
		if !strings.HasPrefix(part.To, "Photos/") || part.From == "" || path.Clean(part.To) != part.To || path.Clean(part.From) != part.From || strings.ContainsAny(part.To+part.From, "\\\x00") || seenParts[part.ID] || seenFrom[part.From] || seenTo[part.To] || i == 1 && part.ID != "motion" {
			return File{}, ErrConflict
		}
		seenParts[part.ID], seenFrom[part.From], seenTo[part.To] = true, true, true
		index := -1
		for j, file := range next {
			if !file.Present {
				continue
			}
			if file.Path == part.To {
				if file.DeviceID != commit.DeviceID || file.DeviceAssetID != commit.DeviceAssetID || file.SourceRevision != commit.SourceRevision {
					return File{}, ErrConflict
				}
				index = j
			}
			if file.Path == part.From {
				if index != -1 {
					return File{}, ErrConflict
				}
				index = j
			}
			if !file.Folder && strings.HasPrefix(part.To, file.Path+"/") {
				return File{}, ErrConflict
			}
		}
		if index < 0 {
			return File{}, ErrNotFound
		}
		file := next[index]
		if file.Path != part.To {
			alreadyCommitted = false
		}
		if file.Folder || file.Hash != part.Hash || file.Size != part.Size {
			return File{}, ErrRevisionMismatch
		}
		if file.Revision == ^uint64(0) {
			return File{}, ErrRevisionOverflow
		}
		file.Path, file.DeviceID, file.DeviceAssetID, file.SourceRevision = part.To, commit.DeviceID, commit.DeviceAssetID, commit.SourceRevision
		file.PhotoComponents = nil
		file.Hidden = commit.Hidden
		if commit.OpaqueOriginal && part.ID == "original" || part.MediaType == "image/dng" {
			file.PhotoPreviewUnsupported = true
		}
		if commit.Capture != nil && !file.CaptureUserCorrected && (file.CaptureSource == "" || file.CaptureSource == "client") {
			file.CaptureTime, file.CaptureOffsetMinutes, file.CaptureSource = commit.Capture.Time, commit.Capture.OffsetMinutes, commit.Capture.Source
		}
		file.Revision++
		next[index] = file
		indices[i] = index
		components[i] = PhotoComponent{ID: part.ID, AssetID: file.EntryID, MediaType: part.MediaType}
	}
	if alreadyCommitted && len(c.files[indices[0]].PhotoComponents) == len(commit.Files) {
		return cloneFile(c.files[indices[0]]), nil
	}
	primary := next[indices[0]].EntryID
	for i, index := range indices {
		if i == 0 {
			next[index].PhotoComponents = components
			next[index].PhotoProcessingPending = true
		} else {
			next[index].PhotoParentID = primary
		}
	}
	previousCollections := c.collections
	if err := c.mapPhotoSourceLocked(commit, next[indices[0]]); err != nil {
		return File{}, err
	}
	defer func() { c.collections = previousCollections }()
	previousAlbums := c.albums
	c.albums = cloneAlbums(c.albums)
	for _, id := range commit.AlbumIDs {
		found := false
		for i := range c.albums {
			album := &c.albums[i]
			if album.ID != id {
				continue
			}
			found = true
			member := false
			for _, assetID := range album.AssetIDs {
				member = member || assetID == primary
			}
			if !member {
				if len(album.AssetIDs) >= 100000 || album.Revision == ^uint64(0) {
					c.albums = previousAlbums
					return File{}, ErrAlbumInvalid
				}
				album.AssetIDs = append(album.AssetIDs, primary)
				album.Revision++
			}
		}
		if !found {
			c.albums = previousAlbums
			return File{}, ErrAlbumNotFound
		}
	}
	if err := c.saveFilesLocked(next); err != nil {
		c.albums = previousAlbums
		return File{}, err
	}
	c.files = next
	previousCollections = c.collections
	return cloneFile(next[indices[0]]), nil
}
