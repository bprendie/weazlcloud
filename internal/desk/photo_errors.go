package desk

import (
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/vault"
	"net/http"
)

func photoAPIError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, previewrpc.ErrEnvironment), errors.Is(err, previewrpc.ErrUnavailable), errors.Is(err, previewrpc.ErrTimeout):
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "30")
	case errors.Is(err, vault.ErrLocked):
		status = http.StatusUnauthorized
	case errors.Is(err, catalog.ErrNotFound) || errors.Is(err, catalog.ErrAlbumNotFound):
		status = http.StatusNotFound
	case errors.Is(err, catalog.ErrRevisionMismatch) || errors.Is(err, library.ErrPhotoCursorStale):
		status = http.StatusConflict
	case errors.Is(err, quota.ErrExceeded):
		status = http.StatusInsufficientStorage
	case errors.Is(err, library.ErrPreviewTooLarge), errors.Is(err, previewrpc.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": previewrpc.ErrorCode(err)})
}
