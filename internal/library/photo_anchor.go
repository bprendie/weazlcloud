package library

import (
	"sort"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

type photoAnchor struct {
	ID       string     `json:"id"`
	Captured *time.Time `json:"captured,omitempty"`
	Modified time.Time  `json:"modified"`
	Imported time.Time  `json:"imported"`
}

func anchorOfPhoto(file catalog.File) *photoAnchor {
	return &photoAnchor{ID: file.EntryID, Captured: file.CaptureTime, Modified: file.Mtime, Imported: file.ImportedAt}
}

// Seek finds the insertion point of a deleted anchor. A forward cursor resumes
// there; a backward cursor ends there, so neither silently drops a neighbor.
func (view photoQueryView) Seek(anchor *photoAnchor, mode string) int {
	if anchor == nil {
		return -1
	}
	file := catalog.File{EntryID: anchor.ID, CaptureTime: anchor.Captured, Mtime: anchor.Modified, ImportedAt: anchor.Imported}
	return sort.Search(view.Len(), func(index int) bool {
		current := view.At(index)
		if mode == "recent" {
			if !current.ImportedAt.Equal(file.ImportedAt) {
				return !current.ImportedAt.After(file.ImportedAt)
			}
			return current.EntryID >= file.EntryID
		}
		return !photoOrderBefore(current, file)
	})
}

func photoOrderBefore(left, right catalog.File) bool {
	leftTime, leftKnown := photos.CanonicalCaptureTime(captureOf(left))
	rightTime, rightKnown := photos.CanonicalCaptureTime(captureOf(right))
	if !leftKnown && rightKnown {
		return false
	}
	if leftKnown && !rightKnown {
		return true
	}
	if leftKnown && rightKnown && !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	if !left.Mtime.Equal(right.Mtime) {
		return left.Mtime.After(right.Mtime)
	}
	return left.EntryID < right.EntryID
}
