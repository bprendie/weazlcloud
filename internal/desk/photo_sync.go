package desk

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (h *Handler) photoSync(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	hidden := r.URL.Query().Get("hidden") == "1"
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" && r.URL.Query().Get("resume") == "1" {
		device, authErr := h.users.DeviceForRequest(r)
		if authErr != nil {
			apiUsersError(w, authErr)
			return
		}
		cursor, err = res.Lib.PhotoDeviceCheckpoint(r.Context(), device.ID, hidden)
		if err != nil {
			photoSyncError(w, err)
			return
		}
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sync page limit"})
			return
		}
	}
	page, err := res.Lib.PhotoSync(r.Context(), cursor, limit, hidden)
	if err != nil {
		photoSyncError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) photoSyncCheckpoint(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	device, err := h.users.DeviceForRequest(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Checkpoint string `json:"checkpoint"`
		Hidden     bool   `json:"hidden"`
	}
	if !decodeBody(w, r, &body, 8192) {
		return
	}
	if err := res.Lib.AcknowledgePhotoSync(r.Context(), device.ID, body.Checkpoint, body.Hidden); err != nil {
		photoSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func photoSyncError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrSyncExpired) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "resync_required": true})
		return
	}
	apiError(w, err)
}
