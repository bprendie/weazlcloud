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
