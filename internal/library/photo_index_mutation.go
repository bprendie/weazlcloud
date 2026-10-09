package library

import "github.com/bprendie/weazlcloud/internal/catalog"

func (l *Library) putPhotoPathLocked(name string, file catalog.File, keep bool) bool {
	index, exists := l.photoByPath[name]
	if !keep {
		delete(l.photoHiddenFolders, name)
		return l.removePhotoPathLocked(name)
	}
	if !exists {
		l.indexPhotoContentLocked(file, false)
		index = len(l.photoRows)
		l.photoRows = append(l.photoRows, file)
		l.photoByPath[file.Path] = index
		l.photoByID[file.EntryID] = file
		if file.Hidden {
			l.photoHiddenFolders[file.Path] = true
		}
		if !file.Folder && file.PhotoParentID == "" && photoMedia(file.Path) {
			if file.Archived {
				l.photoArchivedCount++
			}
			l.photoMediaByPath[file.Path] = len(l.photoMediaRows)
			l.photoMediaRows = append(l.photoMediaRows, file)
		}
		return true
	}
	old := l.photoRows[index]
	l.indexPhotoContentLocked(old, true)
	l.photoRows[index] = file
	l.indexPhotoContentLocked(file, false)
	if old.Hidden {
		delete(l.photoHiddenFolders, old.Path)
	}
	if file.Hidden {
		l.photoHiddenFolders[file.Path] = true
	}
	delete(l.photoByID, old.EntryID)
	l.photoByID[file.EntryID] = file
	wasMedia := !old.Folder && old.PhotoParentID == "" && photoMedia(old.Path)
	isMedia := !file.Folder && file.PhotoParentID == "" && photoMedia(file.Path)
	switch {
	case wasMedia && isMedia:
		if old.Archived != file.Archived {
			if file.Archived {
				l.photoArchivedCount++
			} else {
				l.photoArchivedCount--
			}
		}
		mediaIndex := l.photoMediaByPath[old.Path]
		l.photoMediaRows[mediaIndex] = file
		if old.Path != file.Path {
			delete(l.photoMediaByPath, old.Path)
			l.photoMediaByPath[file.Path] = mediaIndex
		}
	case wasMedia:
		l.removePhotoMediaLocked(old.Path)
	case isMedia:
		l.photoMediaByPath[file.Path] = len(l.photoMediaRows)
		if file.Archived {
			l.photoArchivedCount++
		}
		l.photoMediaRows = append(l.photoMediaRows, file)
	}
	if old.Path != file.Path {
		delete(l.photoByPath, old.Path)
		l.photoByPath[file.Path] = index
	}
	return true
}

func (l *Library) removePhotoPathLocked(path string) bool {
	index, exists := l.photoByPath[path]
	if !exists {
		return false
	}
	old := l.photoRows[index]
	l.indexPhotoContentLocked(old, true)
	if old.Hidden {
		delete(l.photoHiddenFolders, old.Path)
	}
	delete(l.photoByID, old.EntryID)
	delete(l.photoByPath, path)
	last := len(l.photoRows) - 1
	if index != last {
		l.photoRows[index] = l.photoRows[last]
		l.photoByPath[l.photoRows[index].Path] = index
	}
	l.photoRows = l.photoRows[:last]
	if !old.Folder && old.PhotoParentID == "" && photoMedia(old.Path) {
		l.removePhotoMediaLocked(path)
	}
	return true
}

func (l *Library) removePhotoMediaLocked(path string) {
	index, exists := l.photoMediaByPath[path]
	if !exists {
		return
	}
	if l.photoMediaRows[index].Archived {
		l.photoArchivedCount--
	}
	delete(l.photoMediaByPath, path)
	last := len(l.photoMediaRows) - 1
	if index != last {
		l.photoMediaRows[index] = l.photoMediaRows[last]
		l.photoMediaByPath[l.photoMediaRows[index].Path] = index
	}
	l.photoMediaRows = l.photoMediaRows[:last]
}
