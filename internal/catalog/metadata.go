package catalog

import (
	"errors"
	"time"
)

var (
	ErrCaptureRevisionMismatch = errors.New("catalog capture metadata changed")
	ErrInvalidCaptureMetadata  = errors.New("catalog capture metadata is invalid")
)

type CaptureMetadata struct {
	Time          *time.Time
	OffsetMinutes *int
	Source        string
	UserCorrected bool
}

func (c *Catalog) UpdateCapture(entryID string, revision uint64, metadata CaptureMetadata) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updatePhotoMetadataLocked(entryID, revision, &metadata, nil, nil)
}

func sameCapture(file File, metadata CaptureMetadata) bool {
	return timeEqual(file.CaptureTime, metadata.Time) &&
		intEqual(file.CaptureOffsetMinutes, metadata.OffsetMinutes) &&
		file.CaptureSource == metadata.Source &&
		file.CaptureUserCorrected == metadata.UserCorrected
}

func sameCaptureFile(left, right File) bool {
	return timeEqual(left.CaptureTime, right.CaptureTime) &&
		intEqual(left.CaptureOffsetMinutes, right.CaptureOffsetMinutes) &&
		left.CaptureSource == right.CaptureSource &&
		left.CaptureUserCorrected == right.CaptureUserCorrected &&
		left.Camera == right.Camera && left.Width == right.Width && left.Height == right.Height &&
		left.DurationMillis == right.DurationMillis && left.Orientation == right.Orientation &&
		left.PreferredPhoto == right.PreferredPhoto && left.UserRotation == right.UserRotation && left.Favorite == right.Favorite && left.Archived == right.Archived && left.Caption == right.Caption
}

type MediaMetadata struct {
	Camera         string
	Width          int
	Height         int
	DurationMillis int64
	Orientation    int
	UserRotation   int
	Favorite       bool
	Archived       bool
	Caption        string
}

func (c *Catalog) UpdateMedia(entryID string, revision uint64, metadata MediaMetadata) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updatePhotoMetadataLocked(entryID, revision, nil, &metadata, &metadata)
}

// UpdatePhotoMetadata applies capture and derived media fields in one catalog
// revision while preserving owner-edited fields.
func (c *Catalog) UpdatePhotoMetadata(entryID string, revision uint64, capture *CaptureMetadata, media *MediaMetadata) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updatePhotoMetadataLocked(entryID, revision, capture, media, nil)
}

// UpdatePhotoUserMetadata applies capture and owner-edited fields atomically,
// retaining dimensions and other derived metadata.
func (c *Catalog) UpdatePhotoUserMetadata(entryID string, revision uint64, capture *CaptureMetadata, media MediaMetadata) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updatePhotoMetadataLocked(entryID, revision, capture, nil, &media)
}

func (c *Catalog) updatePhotoMetadataLocked(entryID string, revision uint64, capture *CaptureMetadata, derived *MediaMetadata, user *MediaMetadata) (File, error) {
	if user != nil && (user.UserRotation < 0 || user.UserRotation > 270 || user.UserRotation%90 != 0) {
		return File{}, ErrInvalidCaptureMetadata
	}
	if capture != nil && !validCaptureMetadata(*capture) {
		return File{}, ErrInvalidCaptureMetadata
	}
	next := append([]File(nil), c.files...)
	for i := range next {
		f := &next[i]
		if f.EntryID != entryID || f.Revision != revision || !f.Present || f.Folder {
			continue
		}
		changed := false
		if capture != nil && !(f.CaptureUserCorrected && !capture.UserCorrected) && !sameCapture(*f, *capture) {
			f.CaptureTime = cloneTime(capture.Time)
			f.CaptureOffsetMinutes = cloneInt(capture.OffsetMinutes)
			f.CaptureSource = capture.Source
			f.CaptureUserCorrected = capture.UserCorrected
			changed = true
		}
		if derived != nil && (f.Width != derived.Width || f.Height != derived.Height || f.DurationMillis != derived.DurationMillis || f.Orientation != derived.Orientation || f.Camera != derived.Camera) {
			f.Width, f.Height = derived.Width, derived.Height
			f.DurationMillis, f.Orientation = derived.DurationMillis, derived.Orientation
			f.Camera = derived.Camera
			changed = true
		}
		if user != nil && (f.Favorite != user.Favorite || f.Archived != user.Archived || f.Caption != user.Caption || f.UserRotation != user.UserRotation) {
			f.Favorite, f.Archived, f.Caption = user.Favorite, user.Archived, user.Caption
			f.UserRotation = user.UserRotation
			changed = true
		}
		if !changed {
			return cloneFile(*f), nil
		}
		if f.Revision == ^uint64(0) {
			return File{}, ErrRevisionOverflow
		}
		f.Revision++
		if err := c.saveFilesLocked(next); err != nil {
			return File{}, err
		}
		c.files = next
		return cloneFile(*f), nil
	}
	return File{}, ErrCaptureRevisionMismatch
}

func validCaptureMetadata(metadata CaptureMetadata) bool {
	if metadata.Time != nil && (metadata.Time.IsZero() || metadata.Time.Year() < 1900 || metadata.Time.Year() > 2100) {
		return false
	}
	return metadata.OffsetMinutes == nil || (*metadata.OffsetMinutes >= -24*60 && *metadata.OffsetMinutes <= 24*60)
}

func timeEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

func intEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
