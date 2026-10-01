package library

import (
	"sync"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// BeginPhotoStorageWork pauses preparation for the entire import, including
// gaps between individual staged files. Foreground previews remain available.
func (l *Library) BeginPhotoStorageWork() func() {
	l.stageMu.Lock()
	l.photoImports++
	l.stageMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.stageMu.Lock()
			l.photoImports--
			pending := l.takePendingPhotoIngestLocked()
			metadataPending := len(l.pendingMetadataDirectories) > 0
			l.stageMu.Unlock()
			if metadataPending {
				go l.flushMetadataSidecars()
			}
			if len(pending) > 0 {
				go l.retryPhotoIngest(pending)
			}
		})
	}
}

func (l *Library) deferPhotoIngest(file catalog.File) {
	if l.photoAutoDisabled {
		return
	}
	l.stageMu.Lock()
	if len(l.activeStages) > 0 || l.photoImports > 0 {
		if l.pendingPhotoIngest == nil {
			l.pendingPhotoIngest = make(map[string]catalog.File)
		}
		l.pendingPhotoIngest[file.EntryID] = file
		l.stageMu.Unlock()
		return
	}
	l.stageMu.Unlock()
	go l.retryPhotoIngest([]catalog.File{file})
}

func (l *Library) takePendingPhotoIngestLocked() []catalog.File {
	if len(l.activeStages) != 0 || l.photoImports != 0 || len(l.pendingPhotoIngest) == 0 {
		return nil
	}
	files := make([]catalog.File, 0, len(l.pendingPhotoIngest))
	for _, file := range l.pendingPhotoIngest {
		files = append(files, file)
	}
	l.pendingPhotoIngest = nil
	return files
}
