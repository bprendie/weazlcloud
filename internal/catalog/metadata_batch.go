package catalog

import "errors"

// PhotoMetadataMutation is bound to the exact source observed by a resolver.
type PhotoMetadataMutation struct {
	ID       string
	Revision uint64
	Path     string
	Hash     string
	Capture  *CaptureMetadata
	Media    *MediaMetadata
}

type PhotoMetadataBatch struct {
	Files     []File
	Changed   []File
	Conflicts []string
}

// UpdatePhotoMetadataBatch persists one candidate tree. Conflicting assets are
// skipped independently; a persistence failure commits no in-memory changes.
func (c *Catalog) UpdatePhotoMetadataBatch(updates []PhotoMetadataMutation) (PhotoMetadataBatch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := PhotoMetadataBatch{}
	if len(updates) > 100 {
		return result, errors.New("metadata batch exceeds 100 assets")
	}
	next := append([]File(nil), c.files...)
	positions := make(map[string]int, len(next))
	for i, file := range next {
		positions[file.EntryID] = i
	}
	seen := make(map[string]bool)
	for _, update := range updates {
		i, ok := positions[update.ID]
		if !ok || seen[update.ID] {
			result.Conflicts = append(result.Conflicts, update.ID)
			continue
		}
		seen[update.ID] = true
		file := next[i]
		if !file.Present || file.Folder || file.Revision != update.Revision || file.Path != update.Path || file.Hash != update.Hash {
			result.Conflicts = append(result.Conflicts, update.ID)
			continue
		}
		if update.Capture != nil && !validCaptureMetadata(*update.Capture) {
			return PhotoMetadataBatch{}, ErrInvalidCaptureMetadata
		}
		original := file
		if capture := update.Capture; capture != nil && !file.CaptureUserCorrected && !sameCapture(file, *capture) {
			file.CaptureTime, file.CaptureOffsetMinutes = cloneTime(capture.Time), cloneInt(capture.OffsetMinutes)
			file.CaptureSource, file.CaptureUserCorrected = capture.Source, capture.UserCorrected
		}
		if media := update.Media; media != nil {
			if media.Width > 0 {
				file.Width = media.Width
			}
			if media.Height > 0 {
				file.Height = media.Height
			}
			if media.DurationMillis > 0 {
				file.DurationMillis = media.DurationMillis
			}
			if media.Orientation > 0 {
				file.Orientation = media.Orientation
			}
			if media.Camera != "" {
				file.Camera = media.Camera
			}
		}
		if !sameCaptureFile(original, file) {
			if file.Revision == ^uint64(0) {
				return PhotoMetadataBatch{}, ErrRevisionOverflow
			}
			file.Revision++
			next[i] = file
			result.Changed = append(result.Changed, cloneFile(file))
		}
		result.Files = append(result.Files, cloneFile(file))
	}
	if len(result.Changed) != 0 {
		if err := c.saveFilesLocked(next); err != nil {
			return PhotoMetadataBatch{}, err
		}
		c.files = next
	}
	return result, nil
}
