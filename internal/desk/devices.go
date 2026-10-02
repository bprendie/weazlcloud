package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) photoDevices(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet {
		devices := h.users.Devices(user.ID)
		if r.Header.Get("Authorization") != "" {
			d, err := h.users.DeviceForRequest(r)
			if err != nil {
				mobileIdentityError(w, err)
				return
			}
			devices = []users.Device{d}
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
		return
	}
	// Enrolment requires an existing account session and unlocked vault. A stolen
	// device credential cannot mint more credentials.
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account session required"})
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body struct {
		Name   string    `json:"name"`
		Scopes *[]string `json:"scopes"`
	}
	if !decodeBody(w, r, &body, 2048) {
		return
	}
	var scopes []string
	if body.Scopes != nil {
		scopes = append([]string{}, (*body.Scopes)...)
	}
	device, token, err := h.users.CreateScopedDevice(user.ID, body.Name, scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"device": device, "token": token, "scopes": device.GrantedScopes()}
	if device.Scopes == nil {
		out["scope"] = "photos:v1"
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) revokePhotoDevice(w http.ResponseWriter, r *http.Request) {
	user, err := h.users.Current(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body, 2048) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		device, err := h.users.DeviceForRequest(r)
		if err != nil || device.ID != body.ID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "device can only revoke itself"})
			return
		}
	}
	if err := h.users.RevokeDevice(user.ID, body.ID); err != nil {
		apiUsersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
