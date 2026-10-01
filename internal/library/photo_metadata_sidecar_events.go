package library

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) needsPhotoProcessing(name string) bool {
	return !l.photoAutoDisabled && inPhotoRoot(name) && (photoMedia(name) || strings.HasSuffix(strings.ToLower(name), ".json"))
}

// Coalesce imported sidecars by directory. Late ZIPs must repair media which
// arrived earlier; no filename guessing or source reads occur under Library.mu.
func (l *Library) noteMetadataSidecar(name string) {
	if l.photoAutoDisabled {
		return
	}
	l.stageMu.Lock()
	if l.pendingMetadataDirectories == nil {
		l.pendingMetadataDirectories = map[string]bool{}
	}
	l.pendingMetadataDirectories[path.Dir(name)] = true
	busy := l.photoImports > 0 || len(l.activeStages) > 0
	l.stageMu.Unlock()
	if !busy {
		go l.flushMetadataSidecars()
	}
}
func (l *Library) flushMetadataSidecars() {
	ctx, release := l.previewContext(context.Background())
	defer release()
	for ctx.Err() == nil {
		l.stageMu.Lock()
		if l.photoImports > 0 || len(l.activeStages) > 0 {
			l.stageMu.Unlock()
			return
		}
		directories := l.pendingMetadataDirectories
		l.pendingMetadataDirectories = nil
		l.stageMu.Unlock()
		if len(directories) == 0 {
			return
		}
		files := []catalog.File{}
		sidecars := []catalog.File{}
		l.mu.Lock()
		err := l.ensure(ctx)
		if err == nil {
			for dir := range directories {
				for _, file := range l.catalog.Children(dir) {
					if file.PhotoProcessingPending && strings.HasSuffix(strings.ToLower(file.Path), ".json") {
						sidecars = append(sidecars, file)
					}
					if !file.Folder && file.PhotoParentID == "" && photoMedia(file.Path) {
						files = append(files, file)
					}
				}
			}
		}
		l.mu.Unlock()
		if err == nil {
			err = l.queueMetadataIngest(files, true)
		}
		if err == nil {
			err = l.ackPhotoIngest(sidecars)
		}
		if err != nil {
			l.stageMu.Lock()
			if l.pendingMetadataDirectories == nil {
				l.pendingMetadataDirectories = map[string]bool{}
			}
			for dir := range directories {
				l.pendingMetadataDirectories[dir] = true
			}
			l.stageMu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}
