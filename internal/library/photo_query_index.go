package library

import (
	"sort"
	"strconv"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// Filter views retain only row positions and stable-ID lookups. Warm date,
// album and favorites pages copy their bounded output, not the full collection.
type photoQueryView struct {
	Rows    []catalog.File
	Indices []int
	ByID    map[string]int
	Groups  map[string][]int
	Direct  bool
}

func (view photoQueryView) Len() int {
	if view.Direct {
		return len(view.Rows)
	}
	return len(view.Indices)
}
func (view photoQueryView) At(index int) catalog.File {
	if view.Direct {
		return view.Rows[index]
	}
	return view.Rows[view.Indices[index]]
}
func (view photoQueryView) Index(id string) int {
	if index, ok := view.ByID[id]; ok {
		return index
	}
	return -1
}

func (l *Library) photoQueryLocked(album, mode, date string, members map[string]bool, albumRevision uint64) photoQueryView {
	if album == "" && mode == "all" && date == "" && len(l.photoHiddenFolders) == 0 && l.photoArchivedCount == 0 {
		return photoQueryView{Rows: l.photoMediaRows, ByID: l.photoMediaByID, Direct: true}
	}
	if l.photoQueryEpoch != l.photoEpoch || l.photoQueryCache == nil {
		l.photoQueryCache = make(map[string]photoQueryView)
		l.photoQueryOrder = nil
		l.photoQueryEpoch = l.photoEpoch
	}
	key := strings.Join([]string{album, mode, date, strconv.FormatUint(albumRevision, 10)}, "\x00")
	if view, ok := l.photoQueryCache[key]; ok {
		return view
	}
	view := photoQueryView{Rows: l.photoMediaRows, Indices: make([]int, 0), ByID: make(map[string]int)}
	for index, file := range l.photoMediaRows {
		if !l.photoQueryMatchLocked(file, album, mode, date, members) {
			continue
		}
		view.Indices = append(view.Indices, index)
	}
	if mode == "recent" {
		sort.Slice(view.Indices, func(i, j int) bool {
			left, right := view.Rows[view.Indices[i]], view.Rows[view.Indices[j]]
			if !left.ImportedAt.Equal(right.ImportedAt) {
				return left.ImportedAt.After(right.ImportedAt)
			}
			return left.EntryID < right.EntryID
		})
	}
	for index := range view.Indices {
		view.ByID[view.At(index).EntryID] = index
	}
	if len(l.photoQueryOrder) >= 16 {
		delete(l.photoQueryCache, l.photoQueryOrder[0])
		l.photoQueryOrder = l.photoQueryOrder[1:]
	}
	l.photoQueryOrder = append(l.photoQueryOrder, key)
	l.photoQueryCache[key] = view
	return view
}

func (l *Library) photoQueryMatchLocked(file catalog.File, album, mode, date string, members map[string]bool) bool {
	hidden := l.photoPathHiddenLocked(file.Path)
	if hidden != (mode == "hidden") {
		return false
	}
	if mode == "archived" && !file.Archived || album == "" && mode != "hidden" && mode != "archived" && file.Archived {
		return false
	}
	if members != nil {
		if !members[file.EntryID] {
			return false
		}
	} else if album != "" && !strings.HasPrefix(file.Path, album+"/") {
		return false
	}
	if mode == "favorites" && !file.Favorite {
		return false
	}
	if date == "unknown" {
		return file.CaptureTime == nil
	}
	if date != "" {
		if file.CaptureTime == nil {
			return false
		}
		layout := "2006-01-02"
		if len(date) == 4 {
			layout = "2006"
		} else if len(date) == 7 {
			layout = "2006-01"
		}
		if photoCaptureDate(file, layout) != date {
			return false
		}
	}
	return true
}
