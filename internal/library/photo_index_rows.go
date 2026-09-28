package library

import (
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) rebuildPhotoLookupsLocked() {
	l.photoByID = make(map[string]catalog.File, len(l.photoRows))
	l.photoByPath = make(map[string]int, len(l.photoRows))
	l.photoMediaRows = make([]catalog.File, 0, len(l.photoRows))
	l.photoMediaByPath = make(map[string]int, len(l.photoRows))
	for i, f := range l.photoRows {
		l.photoByID[f.EntryID] = f
		l.photoByPath[f.Path] = i
		if !f.Folder && photoMedia(f.Path) {
			l.photoMediaByPath[f.Path] = len(l.photoMediaRows)
			l.photoMediaRows = append(l.photoMediaRows, f)
		}
	}
	sortPhotoRows(l.photoMediaRows)
	l.reindexPhotoMediaLocked()
}

func (l *Library) reindexPhotoMediaLocked() {
	l.photoMediaByPath = make(map[string]int, len(l.photoMediaRows))
	for i, f := range l.photoMediaRows {
		l.photoMediaByPath[f.Path] = i
	}
}

func sortPhotoRows(rows []catalog.File) {
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Mtime.Equal(rows[j].Mtime) {
			return rows[i].Mtime.After(rows[j].Mtime)
		}
		return rows[i].EntryID < rows[j].EntryID
	})
}

func samePhotoRows(current, cached []catalog.File) bool {
	if len(current) != len(cached) {
		return false
	}
	for i := range current {
		a, b := current[i], cached[i]
		if a.EntryID != b.EntryID || a.Revision != b.Revision || a.Path != b.Path || a.Folder != b.Folder || a.Size != b.Size || a.Hash != b.Hash || !a.Mtime.Equal(b.Mtime) {
			return false
		}
	}
	return true
}
