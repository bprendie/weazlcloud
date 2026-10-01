package library

import "strings"

// Change describes a committed catalog mutation. Paths are the affected
// paths; a rename carries the old path followed by the new path.
type Change struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

// ChangeSink receives changes after the catalog has published them. The
// library package keeps this interface small so protocol packages can share
// one notification hub without creating an import cycle.
type ChangeSink interface {
	Publish(Change)
}

func (l *Library) SetChangeSink(sink ChangeSink) {
	l.changeMu.Lock()
	l.changeSink = sink
	l.changeMu.Unlock()
}

func (l *Library) publishChange(change Change) {
	// Folder copies/restores affect every descendant, including preview jobs.
	if change.Kind == "copy" || change.Kind == "restore" {
		paths := append([]string(nil), change.Paths...)
		seen := make(map[string]bool)
		for _, name := range paths {
			seen[name] = true
		}
		for _, file := range l.catalog.List() {
			for _, root := range paths {
				if strings.HasPrefix(file.Path, root+"/") && !seen[file.Path] {
					change.Paths = append(change.Paths, file.Path)
					seen[file.Path] = true
				}
			}
		}
	}
	l.updatePhotoIndex(change)
	if change.Kind == "put" || change.Kind == "restore" || change.Kind == "copy" {
		for _, name := range change.Paths {
			if strings.HasPrefix(name, PhotosRoot) {
				if file, ok := l.catalog.Get(name); ok && !file.Folder && photoPreviewable(file.Path) {
					l.deferPhotoIngest(file)
				}
			}
		}
	}
	l.changeMu.RLock()
	sink := l.changeSink
	l.changeMu.RUnlock()
	if sink != nil {
		sink.Publish(change)
	}
}
