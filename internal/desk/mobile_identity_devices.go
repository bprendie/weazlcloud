package desk

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/users"
)

func mobileDeviceID(r *http.Request) string {
	return strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/devices/"), "/")[0]
}
func (h *Handler) mobileDeviceStatus(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	for _, d := range h.users.Devices(u.ID) {
		if d.ID == mobileDeviceID(r) {
			writeJSON(w, 200, map[string]any{"device": d, "grants": d.GrantedScopes()})
			return
		}
	}
	mobileIdentityError(w, users.ErrNoSession)
}
func (h *Handler) mobileDeviceRotate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OperationID        string `json:"operation_id"`
		ExpectedGeneration uint64 `json:"expected_generation"`
		ReplacementToken   string `json:"replacement_token"`
	}
	if !decodeBody(w, r, &body, 2048) {
		return
	}
	d, err := h.users.RotateDevice(r, mobileDeviceID(r), body.OperationID, body.ExpectedGeneration, body.ReplacementToken)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"device": d, "generation": d.Generation})
}
func (h *Handler) mobileDeviceReauthorize(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		mobileIdentityError(w, users.ErrInsufficientScope)
		return
	}
	res, _, err := h.currentResource(r)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		writeJSON(w, 401, map[string]string{"error": "vault locked", "code": "vault_locked"})
		return
	}
	var body struct {
		ReplacementToken string    `json:"replacement_token"`
		Scopes           *[]string `json:"scopes"`
	}
	if !decodeBody(w, r, &body, 2048) {
		return
	}
	var scopes []string
	if body.Scopes != nil {
		scopes = append([]string{}, (*body.Scopes)...)
	}
	d, err := h.users.ReauthorizeDevice(r, mobileDeviceID(r), body.ReplacementToken, scopes)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"device": d, "grants": d.GrantedScopes()})
}
func (h *Handler) mobileDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	if err := h.users.RevokeDevice(u.ID, mobileDeviceID(r)); err != nil {
		mobileIdentityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "revoked"})
}
func mobileIdentityError(w http.ResponseWriter, err error) {
	status, code, message := 503, "retryable_service_unavailable", "identity service unavailable"
	switch {
	case errors.Is(err, users.ErrNoSession):
		status, code, message = 401, "reauthorization_required", "authentication required"
	case errors.Is(err, users.ErrInsufficientScope):
		status, code, message = 403, "insufficient_scope", "insufficient scope"
	case errors.Is(err, users.ErrRotationConflict):
		status, code, message = 409, "idempotency_conflict", "credential generation or operation conflict"
	case errors.Is(err, users.ErrInvalidRotation):
		status, code, message = 400, "invalid_request", "invalid credential rotation"
	case errors.Is(err, users.ErrDeviceLimit):
		status, code, message = 409, "device_limit", "revoke an old device before adding another"
	}
	if status == 503 {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, map[string]any{"error": message, "code": code, "retryable": status == 503})
}
