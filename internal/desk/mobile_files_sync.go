package desk

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

func (h *Handler) mobileFilesSync(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	device, err := h.users.DeviceForRequest(r)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	if device.OwnerID != user.ID || r.URL.Query().Get("device_id") != "" && r.URL.Query().Get("device_id") != device.ID {
		nativeJSONcode(w, 403, "device_mismatch", "authenticated device required")
		return
	}
	prefix, cursor := r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor")
	if cursor == "" && r.URL.Query().Get("resume") == "1" {
		cursor, err = res.Lib.FilesDeviceCheckpoint(r.Context(), device.ID, prefix)
		if err != nil {
			mobileFilesError(w, err)
			return
		}
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			nativeJSONcode(w, 400, "invalid_request", "limit must be between 1 and 200")
			return
		}
	}
	page, err := res.Lib.FilesSync(r.Context(), device.ID, cursor, prefix, limit)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (h *Handler) mobileFilesCheckpoint(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	device, err := h.users.DeviceForRequest(r)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	var body struct {
		Checkpoint string `json:"checkpoint"`
		Prefix     string `json:"prefix"`
		DeviceID   string `json:"device_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		nativeJSONcode(w, 400, "invalid_request", "invalid checkpoint body")
		return
	}
	if device.OwnerID != user.ID || body.DeviceID != "" && body.DeviceID != device.ID || r.URL.Query().Get("device_id") != "" && r.URL.Query().Get("device_id") != device.ID {
		nativeJSONcode(w, 403, "device_mismatch", "authenticated device required")
		return
	}
	if err := res.Lib.AcknowledgeFilesSync(r.Context(), device.ID, body.Checkpoint, body.Prefix); err != nil {
		mobileFilesError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "acknowledged"})
}
