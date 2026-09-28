package library

import "sync"

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
			l.stageMu.Unlock()
		})
	}
}
