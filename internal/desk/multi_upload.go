package desk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type uploadCreateBody struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"`
}

func (h *Handler) multiCreateUpload(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if res.Vault == nil || !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body uploadCreateBody
	if !decodeBody(w, r, &body, 8192) {
		return
	}
	view, err := h.uploads.Create(user, body.Path, body.Size, strings.ToLower(strings.TrimSpace(body.Hash)))
	if err != nil {
		uploadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (h *Handler) multiUploadRoute(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if res.Vault == nil || !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/uploads/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "finalize") {
		uploadError(w, upload.ErrNotFound)
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			uploadError(w, errors.New("upload finalize requires POST"))
			return
		}
		h.multiFinalizeUpload(w, r, res, user, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := h.uploads.Status(user, id)
		if err != nil {
			uploadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodPatch:
		h.multiAppendUpload(w, r, res, user, id)
	case http.MethodDelete:
		if err := h.uploads.Cancel(user, id); err != nil {
			uploadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	default:
		uploadError(w, errors.New("unsupported upload operation"))
	}
}

func (h *Handler) multiAppendUpload(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, user users.User, id string) {
	offset, err := parseUploadOffset(r.Header.Get("Upload-Offset"))
	if err != nil {
		uploadError(w, err)
		return
	}
	length := r.ContentLength
	reserveBytes := length
	if reserveBytes < 0 || reserveBytes > upload.MaxChunkBytes {
		reserveBytes = upload.MaxChunkBytes
	}
	release, err := h.uploads.Reserve(user, reserveBytes, 0, res)
	if err != nil {
		uploadError(w, err)
		return
	}
	defer release()
	view, err := h.uploads.Append(r.Context(), user, id, offset, length, r.Body)
	if err != nil {
		uploadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) multiFinalizeUpload(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, user users.User, id string) {
	var file fileView
	view, err := h.uploads.Finalize(r.Context(), user, id, func(ctx context.Context, session upload.SessionView, body io.Reader) error {
		if err := res.Lib.Ensure(ctx); err == nil {
			if old, metadataErr := res.Lib.Metadata(ctx, session.Path); metadataErr == nil && old.Hash == session.Hash && old.Size == session.Size {
				file = fileView{Path: old.Path, Size: old.Size, Mtime: old.Mtime}
				return nil
			}
		}
		current := int64(0)
		if old, metadataErr := res.Lib.Metadata(ctx, session.Path); metadataErr == nil {
			current = old.Size
		}
		release, reserveErr := h.uploads.Reserve(user, session.Size, current, res)
		if reserveErr != nil {
			return reserveErr
		}
		defer release()
		stored, putErr := res.Lib.PutReader(ctx, session.Path, body, session.Size)
		if putErr == nil {
			file = fileView{Path: stored.Path, Size: stored.Size, Mtime: stored.Mtime}
		}
		return putErr
	})
	if err != nil {
		uploadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload": view, "file": file})
}

func parseUploadOffset(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, errors.New("Upload-Offset header is required")
	}
	var offset int64
	if _, err := fmt.Sscanf(raw, "%d", &offset); err != nil || offset < 0 || strings.TrimSpace(fmt.Sprintf("%d", offset)) != strings.TrimSpace(raw) {
		return 0, errors.New("Upload-Offset header is invalid")
	}
	return offset, nil
}

func uploadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var offset *upload.OffsetError
	switch {
	case errors.As(err, &offset):
		status = http.StatusConflict
	case errors.Is(err, upload.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, upload.ErrChunkTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, upload.ErrIncomplete):
		status = http.StatusConflict
	case errors.Is(err, upload.ErrHashMismatch):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, vault.ErrLocked):
		status = http.StatusUnauthorized
	}
	body := map[string]any{"error": err.Error()}
	if offset != nil {
		body["offset"] = offset.Expected
	}
	writeJSON(w, status, body)
}
