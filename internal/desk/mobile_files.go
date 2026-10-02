package desk

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// tryMobileFiles is wired by the parent router. It owns only native Files routes.
func (h *Handler) tryMobileFiles(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/files/") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if h.users == nil || h.registry == nil {
		nativeJSONcode(w, 401, "authentication_required", "owner account required")
		return true
	}
	user, err := h.users.Current(r)
	if err != nil {
		mobileFilesError(w, err)
		return true
	}
	ctx, release, ok := h.registry.Enter(r.Context(), user.ID)
	if !ok {
		mobileFilesError(w, users.ErrNoSession)
		return true
	}
	defer release()
	r = r.WithContext(ctx)
	current, ok := h.users.User(user.ID)
	if !ok || current.Disabled || current.Deleting {
		mobileFilesError(w, users.ErrNoSession)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !mutating(r) {
		nativeJSONcode(w, 403, "forbidden", "request validation required")
		return true
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
	switch {
	case rest == "sync" && r.Method == http.MethodGet:
		h.mobileFilesSync(w, r)
	case rest == "sync/checkpoint" && r.Method == http.MethodPost:
		h.mobileFilesCheckpoint(w, r)
	case strings.HasSuffix(rest, "/content") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		id := strings.TrimSuffix(rest, "/content")
		if !mobileFilesID(id) {
			nativeJSONcode(w, 404, "not_found", "file not found")
			break
		}
		h.mobileFileContent(w, r, id)
	case mobileFilesID(rest) && r.Method == http.MethodGet:
		res, user, err := h.currentResource(r)
		if err != nil {
			mobileFilesError(w, err)
			break
		}
		item, err := res.Lib.MobileFileMetadata(r.Context(), rest)
		if err != nil {
			mobileFilesError(w, err)
			break
		}
		etag := mobileFilesETag(user.ID, item)
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", item.Mtime.UTC().Format(http.TimeFormat))
		if status := mobileFilesPrecondition(r, etag, item.Mtime); status != 0 {
			if status == http.StatusPreconditionFailed {
				nativeJSONcode(w, status, "precondition_failed", "file revision precondition failed")
			} else {
				w.WriteHeader(status)
			}
			break
		}
		writeJSON(w, 200, item)
	default:
		nativeJSONcode(w, 404, "not_found", "native Files route not found")
	}
	return true
}

func mobileFilesID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, ch := range id {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

// Files errors carry native codes without changing any browser error helper.
func nativeJSONcode(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, status, map[string]any{"error": message, "code": code, "resync_required": code == "resync_required"})
}

func mobileFilesError(w http.ResponseWriter, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, users.ErrInsufficientScope):
		status, code = 403, "insufficient_scope"
	case errors.Is(err, users.ErrNoSession):
		status, code = 401, "authentication_required"
	case errors.Is(err, vault.ErrLocked):
		status, code = 401, "vault_locked"
	case errors.Is(err, catalog.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, catalog.ErrSyncExpired):
		status, code = 409, "resync_required"
	}
	nativeJSONcode(w, status, code, err.Error())
}
