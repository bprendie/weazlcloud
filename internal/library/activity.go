package library

func (l *Library) SetActivityTracker(track func() func()) {
	l.activityMu.Lock()
	l.activity = track
	l.activityMu.Unlock()
}

func (l *Library) trackStorage() func() {
	l.activityMu.RLock()
	track := l.activity
	l.activityMu.RUnlock()
	if track == nil {
		return func() {}
	}
	return track()
}
