package library

import "sort"

// Requires photoMu. Initial sync uses a stable-ID index with a binary seek;
// a warm page no longer clones and sorts the full collection on every request.
func (l *Library) photoSyncViewLocked(hidden bool) photoQueryView {
	if l.photoQueryEpoch != l.photoEpoch || l.photoQueryCache == nil {
		l.photoQueryCache = make(map[string]photoQueryView)
		l.photoQueryOrder = nil
		l.photoQueryEpoch = l.photoEpoch
	}
	key := "sync:visible"
	if hidden {
		key = "sync:hidden"
	}
	if view, ok := l.photoQueryCache[key]; ok {
		return view
	}
	view := photoQueryView{Rows: l.photoRows, Indices: []int{}}
	for index, file := range l.photoRows {
		if l.photoPathHiddenLocked(file.Path) == hidden && (file.Folder || photoMedia(file.Path)) {
			view.Indices = append(view.Indices, index)
		}
	}
	sort.Slice(view.Indices, func(i, j int) bool { return view.At(i).EntryID < view.At(j).EntryID })
	if len(l.photoQueryOrder) >= 16 {
		delete(l.photoQueryCache, l.photoQueryOrder[0])
		l.photoQueryOrder = l.photoQueryOrder[1:]
	}
	l.photoQueryOrder = append(l.photoQueryOrder, key)
	l.photoQueryCache[key] = view
	return view
}
