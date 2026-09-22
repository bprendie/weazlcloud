package upload

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound      = errors.New("upload session not found")
	ErrIncomplete    = errors.New("upload is incomplete")
	ErrChunkTooLarge = errors.New("upload chunk is too large")
	ErrHashMismatch  = errors.New("upload content hash does not match")
	ErrCorrupt       = errors.New("upload session storage is inconsistent")
)

type OffsetError struct{ Expected int64 }

func (e *OffsetError) Error() string { return fmt.Sprintf("upload offset is %d", e.Expected) }
