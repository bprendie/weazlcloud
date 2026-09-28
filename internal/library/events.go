package library

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
	l.updatePhotoIndex(change)
	l.changeMu.RLock()
	sink := l.changeSink
	l.changeMu.RUnlock()
	if sink != nil {
		sink.Publish(change)
	}
}
