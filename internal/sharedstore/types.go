package sharedstore

import "errors"

var (
	ErrDenied = errors.New("shared object access denied")
	ErrState  = errors.New("shared object operation is not in a readable state")
	ErrFormat = errors.New("unsupported shared object format")
	ErrStale  = errors.New("shared object revision is stale")
)

const (
	formatVersion      = 1
	frameSize          = 1 << 20
	chunkFormatVersion = 2
)

// Reference is owner-scoped metadata intended to live inside an encrypted catalog.
type Reference struct {
	Version   int    `json:"version"`
	ObjectID  string `json:"object_id"`
	EntryID   string `json:"entry_id"`
	Revision  uint64 `json:"revision"`
	Operation string `json:"operation"`
}

type Prepared struct {
	Reference Reference
	Operation string
}

type Options struct {
	FailureHook func(string) error
}

func fail(o Options, point string) error {
	if o.FailureHook != nil {
		return o.FailureHook(point)
	}
	return nil
}
