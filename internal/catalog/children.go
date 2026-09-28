package catalog

import (
	"strings"
)

func indexChildren(files []File) map[string][]File {
	sets := make(map[string]map[string]File)
	put := func(parent string, file File, actual bool) {
		if sets[parent] == nil {
			sets[parent] = make(map[string]File)
		}
		old, exists := sets[parent][file.Path]
		if exists && old.EntryID != "" && !actual {
			return
		}
		if exists && old.EntryID == "" && !actual {
			if old.Mtime.After(file.Mtime) {
				file.Mtime = old.Mtime
			}
		}
		sets[parent][file.Path] = file
	}
	for _, file := range files {
		if !file.Present || file.Path == "" {
			continue
		}
		parts := strings.Split(file.Path, "/")
		parent := ""
		for i := range parts {
			name := strings.Join(parts[:i+1], "/")
			if i == len(parts)-1 {
				put(parent, file, true)
			} else {
				put(parent, File{Path: name, Folder: true, Mtime: file.Mtime, Present: true}, false)
			}
			parent = name
		}
	}
	result := make(map[string][]File, len(sets))
	for parent, set := range sets {
		rows := make([]File, 0, len(set))
		for _, file := range set {
			rows = append(rows, file)
		}
		result[parent] = rows
	}
	return result
}

func indexPaths(files []File) map[string]File {
	index := make(map[string]File, len(files))
	for _, file := range files {
		if file.Present {
			index[file.Path] = file
		}
	}
	return index
}
