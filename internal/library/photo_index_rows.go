package library

import (
	"slices"
	"sort"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func (l *Library) rebuildPhotoLookupsLocked() {
	l.photoContentIndex = make(map[PhotoContentKey][]string)
	l.photoByID = make(map[string]catalog.File, len(l.photoRows))
	l.photoByPath = make(map[string]int, len(l.photoRows))
	l.photoMediaRows = make([]catalog.File, 0, len(l.photoRows))
	l.photoMediaByPath = make(map[string]int, len(l.photoRows))
	l.photoHiddenFolders = make(map[string]bool)
	l.photoArchivedCount = 0
	for i, f := range l.photoRows {
		l.indexPhotoContentLocked(f, false)
		l.photoByID[f.EntryID] = f
		l.photoByPath[f.Path] = i
		if f.Hidden {
			l.photoHiddenFolders[f.Path] = true
		}
		if !f.Folder && f.PhotoParentID == "" && photoMedia(f.Path) {
			if f.Archived {
				l.photoArchivedCount++
			}
			l.photoMediaByPath[f.Path] = len(l.photoMediaRows)
			l.photoMediaRows = append(l.photoMediaRows, f)
		}
	}
	sortPhotoRows(l.photoMediaRows)
	l.reindexPhotoMediaLocked()
}

// photoPathHiddenLocked requires photoMu and applies asset visibility and inherited folder state.
func (l *Library) photoPathHiddenLocked(name string) bool {
	for parent := name; parent != ""; {
		if l.photoHiddenFolders[parent] {
			return true
		}
		i := strings.LastIndexByte(parent, '/')
		if i <= 0 {
			break
		}
		parent = parent[:i]
	}
	return false
}

func photoPathHidden(name string, folders map[string]bool) bool {
	for parent := name; parent != ""; {
		if folders[parent] {
			return true
		}
		i := strings.LastIndexByte(parent, '/')
		if i <= 0 {
			break
		}
		parent = parent[:i]
	}
	return false
}

func (l *Library) reindexPhotoMediaLocked() {
	l.photoMediaByPath = make(map[string]int, len(l.photoMediaRows))
	l.photoMediaByID = make(map[string]int, len(l.photoMediaRows))
	for i, f := range l.photoMediaRows {
		l.photoMediaByPath[f.Path] = i
		l.photoMediaByID[f.EntryID] = i
	}
}

func photoPageAnchorIndex(files []catalog.File, id string, indexed map[string]int, direct bool) int {
	if direct {
		if index, ok := indexed[id]; ok {
			return index
		}
		return -1
	}
	return photoIndexOfID(files, id)
}

func sortPhotoRows(rows []catalog.File) {
	sort.Slice(rows, func(i, j int) bool { return photoOrderBefore(rows[i], rows[j]) })
}

func captureOf(file catalog.File) *photos.Capture {
	if file.CaptureTime == nil {
		return nil
	}
	return &photos.Capture{Time: *file.CaptureTime}
}

func samePhotoRows(current, cached []catalog.File) bool {
	if len(current) != len(cached) {
		return false
	}
	for i := range current {
		a, b := current[i], cached[i]
		if !slices.Equal(a.PhotoComponents, b.PhotoComponents) {
			return false
		}
		if a.PhotoPreviewUnsupported != b.PhotoPreviewUnsupported || a.PhotoParentID != b.PhotoParentID || a.DeviceID != b.DeviceID || a.DeviceAssetID != b.DeviceAssetID || a.SourceRevision != b.SourceRevision || a.EntryID != b.EntryID || a.Revision != b.Revision || a.Path != b.Path || a.Folder != b.Folder || a.Hidden != b.Hidden || a.Size != b.Size || a.Hash != b.Hash || !a.Mtime.Equal(b.Mtime) || !a.ImportedAt.Equal(b.ImportedAt) || !samePhotoCapture(a, b) || a.Width != b.Width || a.Height != b.Height || a.DurationMillis != b.DurationMillis || a.Orientation != b.Orientation || a.Camera != b.Camera || a.UserRotation != b.UserRotation || a.PreferredPhoto != b.PreferredPhoto || a.Favorite != b.Favorite || a.Archived != b.Archived || a.Caption != b.Caption {
			return false
		}
	}
	return true
}

func samePhotoCapture(left, right catalog.File) bool {
	if left.CaptureSource != right.CaptureSource || left.CaptureUserCorrected != right.CaptureUserCorrected {
		return false
	}
	if (left.CaptureTime == nil) != (right.CaptureTime == nil) || (left.CaptureOffsetMinutes == nil) != (right.CaptureOffsetMinutes == nil) {
		return false
	}
	if left.CaptureTime != nil && !left.CaptureTime.Equal(*right.CaptureTime) {
		return false
	}
	return left.CaptureOffsetMinutes == nil || *left.CaptureOffsetMinutes == *right.CaptureOffsetMinutes
}
