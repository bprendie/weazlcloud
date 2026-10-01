package library

import "github.com/bprendie/weazlcloud/internal/catalog"

func (l *Library) photoItemVisible(file catalog.File, hidden bool) PhotoItem {
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	return l.photoItemVisibleLocked(file, hidden)
}

// A Library move can split a pair across folders with different visibility.
// The normal Photos response must not enumerate the newly hidden component.
func (l *Library) photoItemVisibleLocked(file catalog.File, hidden bool) PhotoItem {
	item := photoItemFromFile(file)
	if parent, ok := l.photoByID[file.PhotoParentID]; ok && parent.Present && l.photoPathHiddenLocked(parent.Path) == hidden {
		item.ParentAssetID = parent.EntryID
	}
	item.Components = nil
	for _, part := range file.PhotoComponents {
		component, ok := l.photoByID[part.AssetID]
		if ok && component.Present && l.photoPathHiddenLocked(component.Path) == hidden {
			item.Components = append(item.Components, part)
		}
	}
	return item
}
