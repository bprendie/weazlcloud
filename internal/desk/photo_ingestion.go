package desk

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photoingest"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) v1PhotoUploadCreate(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var spec photoingest.Spec
	if !decodeBody(w, r, &spec, 64<<10) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		device, err := h.users.DeviceForRequest(r)
		if err != nil || spec.DeviceID != "" && spec.DeviceID != device.ID {
			writeJSON(w, 403, map[string]string{"error": "upload device does not match credential"})
			return
		}
		spec.DeviceID = device.ID
	}
	view, err := h.photoUploads.Create(r.Context(), res, user, spec)
	if err != nil {
		photoIngestError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, view)
}

func (h *Handler) v1PhotoUploadRoute(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/photos/uploads/"), "/")
	if len(parts) < 1 || len(parts) > 3 || !validPhotoUploadPart(parts[0]) {
		uploadError(w, upload.ErrNotFound)
		return
	}
	deviceID := ""
	if r.Header.Get("Authorization") != "" {
		device, err := h.users.DeviceForRequest(r)
		if err != nil {
			apiUsersError(w, err)
			return
		}
		deviceID = device.ID
	}
	view, err := h.photoUploads.Status(res, user, parts[0], deviceID)
	if errors.Is(err, upload.ErrNotFound) {
		h.legacyV1PhotoUploadRoute(w, r)
		return
	}
	if err != nil {
		photoIngestError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, view)
		case http.MethodDelete:
			if err := h.photoUploads.Cancel(res, user, parts[0], deviceID); err != nil {
				photoIngestError(w, err)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "cancelled"})
		default:
			writeJSON(w, 405, map[string]string{"error": "unsupported photo upload operation"})
		}
		return
	}
	if len(parts) == 3 && parts[1] == "components" && r.Method == http.MethodPatch {
		offset, err := parseUploadOffset(r.Header.Get("Upload-Offset"))
		if err != nil {
			uploadError(w, err)
			return
		}
		view, err = h.photoUploads.Append(r.Context(), res, user, parts[0], deviceID, parts[2], offset, r.ContentLength, strings.ToLower(r.Header.Get("Upload-Chunk-SHA256")), r.Body)
		if err != nil {
			photoIngestError(w, err)
			return
		}
		writeJSON(w, 200, view)
		return
	}
	if len(parts) == 2 && parts[1] == "finalize" && r.Method == http.MethodPost {
		var body photoUploadFinalizeBody
		if r.ContentLength != 0 && !decodeBody(w, r, &body, 4096) {
			return
		}
		var capture *catalog.CaptureMetadata
		if body.CapturedAt != "" {
			t, err := time.Parse(time.RFC3339Nano, body.CapturedAt)
			if err != nil || t.Year() < 1900 || t.Year() > 2100 {
				photoIngestError(w, photoingest.ErrInvalid)
				return
			}
			capture = &catalog.CaptureMetadata{Time: &t, Source: "client"}
			if body.OffsetKnown != nil && *body.OffsetKnown {
				_, seconds := t.Zone()
				offset := seconds / 60
				capture.OffsetMinutes = &offset
			}
		}
		ctx, guard, cancel, e := h.photoUploadCommitGuard(r, res)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		defer cancel()
		view, err = h.photoUploads.FinalizeGuarded(ctx, res, user, parts[0], deviceID, capture, guard)
		if err != nil {
			photoIngestError(w, err)
			return
		}
		writeJSON(w, 200, view)
		return
	}
	writeJSON(w, 405, map[string]string{"error": "unsupported photo upload operation"})
}

func photoIngestError(w http.ResponseWriter, err error) {
	if errors.Is(err, photoingest.ErrInvalid) {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if errors.Is(err, catalog.ErrAlbumNotFound) || errors.Is(err, catalog.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "photo destination or album not found"})
		return
	}
	uploadError(w, err)
}
