package library

import (
	"github.com/bprendie/weazlcloud/internal/catalog"
	"path/filepath"
	"strings"
)

// Content edits demote the old variants; metadata-only edits keep their warmth.
// Shared-content copies can promote these bytes again on any authorized read.
func (l *Library) markObsoletePreviews(change Change) {
	if change.Kind != "put" && change.Kind != "delete" && change.Kind != "photo-metadata" {
		return
	}
	_, root := thumbnailOwnerAndRoot(l.thumbnailDir())
	thumbnailCacheMu.Lock()
	cache := thumbnailCaches[root]
	thumbnailCacheMu.Unlock()
	if cache == nil {
		return
	}
	l.photoMu.Lock()
	var files []catalog.File
	for _, name := range change.Paths {
		if i, ok := l.photoByPath[name]; ok {
			files = append(files, l.photoRows[i])
		}
		if change.Kind == "delete" {
			for _, f := range l.photoMediaRows {
				if strings.HasPrefix(f.Path, name+"/") {
					files = append(files, f)
				}
			}
		}
	}
	l.photoMu.Unlock()
	for _, old := range files {
		oldKey, err := thumbnailKey(l.vault, old, 0)
		if err != nil {
			continue
		}
		if current, ok := l.catalog.Get(old.Path); ok {
			next, err := thumbnailKey(l.vault, current, 0)
			if err == nil && next == oldKey {
				continue
			}
		}
		keys := []string{oldKey + ".meta"}
		for _, size := range []int{320, 1280} {
			key, err := thumbnailKey(l.vault, old, size)
			if err == nil {
				keys = append(keys, key)
			}
		}
		cache.mu.Lock()
		for _, key := range keys {
			path := filepath.Join(l.thumbnailDir(), key+".enc")
			if f, ok := cache.files[path]; ok {
				f.obsolete = true
				cache.files[path] = f
				cache.touch(path)
			}
		}
		cache.mu.Unlock()
	}
}
