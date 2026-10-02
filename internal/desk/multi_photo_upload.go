package desk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const defaultPhotoRootID = "root:photos"

type photoUploadCreateBody struct {
	DeviceID      string `json:"device_id"`
	DeviceAssetID string `json:"device_asset_id"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	RootID        string `json:"root_id,omitempty"`
}

type photoUploadFinalizeBody struct {
	CapturedAt  string `json:"captured_at,omitempty"`
	OffsetKnown *bool  `json:"offset_known,omitempty"`
}

func (h *Handler) legacyV1PhotoUploadCreate(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if res.Vault == nil || !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body photoUploadCreateBody
	if !decodeBody(w, r, &body, 8192) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		device, err := h.users.DeviceForRequest(r)
		if err != nil || body.DeviceID != "" && body.DeviceID != device.ID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "upload device does not match credential"})
			return
		}
		body.DeviceID = device.ID
	}
	if body.RootID == "" {
		body.RootID = defaultPhotoRootID
	}
	if body.RootID != defaultPhotoRootID || !validPhotoUploadPart(body.DeviceID) || !validPhotoUploadPart(body.DeviceAssetID) || body.Filename == "" || len(body.Filename) > 255 || body.Filename == "." || body.Filename == ".." || strings.ContainsAny(body.Filename, "/\\\x00") || path.Base(body.Filename) != body.Filename || len(strings.TrimSpace(body.SHA256)) != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "photo upload destination is invalid"})
		return
	}
	target, err := library.CleanPath("Photos/Mobile/" + body.DeviceID + "/" + body.DeviceAssetID + "/" + body.Filename)
	if err != nil || !strings.HasPrefix(target, library.PhotosRoot) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "photo upload destination is invalid"})
		return
	}
	view, err := h.uploads.CreateIdempotent(user, target, body.Size, strings.ToLower(strings.TrimSpace(body.SHA256)), "photos:v1:"+body.DeviceID+":"+body.DeviceAssetID)
	if err != nil {
		uploadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload":          view,
		"component":       map[string]any{"id": "original", "offset": view.Offset, "chunk_limit": upload.MaxChunkBytes},
		"device_asset_id": body.DeviceAssetID,
	})
}

func (h *Handler) legacyV1PhotoUploadRoute(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if res.Vault == nil || !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/photos/uploads/"), "/")
	if len(parts) < 1 || len(parts) > 3 || !validPhotoUploadPart(parts[0]) {
		uploadError(w, upload.ErrNotFound)
		return
	}
	id := parts[0]
	view, err := h.uploads.Status(user, id)
	if err == nil && r.Header.Get("Authorization") != "" {
		device, authErr := h.users.DeviceForRequest(r)
		if authErr != nil || !strings.HasPrefix(view.Path, library.PhotosRoot+"Mobile/"+device.ID+"/") {
			uploadError(w, upload.ErrNotFound)
			return
		}
	}
	if err != nil || !strings.HasPrefix(view.Path, library.PhotosRoot+"Mobile/") {
		if err == nil {
			err = upload.ErrNotFound
		}
		uploadError(w, err)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"upload": view, "component": map[string]any{"id": "original", "offset": view.Offset, "chunk_limit": upload.MaxChunkBytes}})
		case http.MethodDelete:
			if err := h.uploads.Cancel(user, id); err != nil {
				uploadError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		default:
			uploadError(w, errors.New("unsupported photo upload operation"))
		}
		return
	}
	if len(parts) == 3 && parts[1] == "components" && parts[2] == "original" && r.Method == http.MethodPatch {
		offset, err := parseUploadOffset(r.Header.Get("Upload-Offset"))
		if err != nil {
			uploadError(w, err)
			return
		}
		updated, err := h.uploads.Append(r.Context(), user, id, offset, r.ContentLength, strings.ToLower(r.Header.Get("Upload-Chunk-SHA256")), r.Body)
		if err != nil {
			uploadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"upload": updated, "component": map[string]any{"id": "original", "offset": updated.Offset, "chunk_limit": upload.MaxChunkBytes}})
		return
	}
	if len(parts) == 2 && parts[1] == "finalize" && r.Method == http.MethodPost {
		h.v1PhotoUploadFinalize(w, r, res, user, id, view.Path)
		return
	}
	uploadError(w, errors.New("unsupported photo upload operation"))
}

func (h *Handler) v1PhotoUploadFinalize(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, user users.User, id, destination string) {
	var body photoUploadFinalizeBody
	if r.ContentLength != 0 && !decodeBody(w, r, &body, 4096) {
		return
	}
	var capture *catalog.CaptureMetadata
	if body.CapturedAt != "" {
		captured, err := time.Parse(time.RFC3339Nano, body.CapturedAt)
		if err != nil || captured.Year() < 1900 || captured.Year() > 2100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capture timestamp is invalid"})
			return
		}
		metadata := catalog.CaptureMetadata{Time: &captured, Source: "client"}
		if body.OffsetKnown != nil && *body.OffsetKnown {
			_, offset := captured.Zone()
			offsetMinutes := offset / 60
			metadata.OffsetMinutes = &offsetMinutes
		}
		capture = &metadata
	}
	ctx, guard, cancel, err := h.photoUploadCommitGuard(r, res)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	defer cancel()
	view, err := h.uploads.Finalize(ctx, user, id, func(ctx context.Context, session upload.SessionView, source io.Reader) error {
		return res.Lib.CommitLegacyPhotoStreamGuarded(ctx, session.ID, session.Path, session.Size, session.Hash, source, capture, guard)
	})
	if err != nil {
		uploadError(w, err)
		return
	}
	file, err := res.Lib.Metadata(r.Context(), destination)
	if err != nil {
		apiError(w, err)
		return
	}
	if capture != nil && guard == nil {
		file, err = res.Lib.SetPhotoCapture(r.Context(), destination, *capture)
		if err != nil {
			apiError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload": view, "asset_id": file.EntryID, "device_asset_id": strings.Split(strings.TrimPrefix(destination, "Photos/Mobile/"), "/")[1], "path": destination, "revision": file.Revision, "status": "stored"})
}

func validPhotoUploadPart(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 100 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}
