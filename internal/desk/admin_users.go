package desk

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type adminUserView struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	FullName       string `json:"full_name,omitempty"`
	Admin          bool   `json:"admin"`
	Disabled       bool   `json:"disabled"`
	DisablePending bool   `json:"disable_pending"`
	Deleting       bool   `json:"deleting"`
	DeleteError    string `json:"delete_error,omitempty"`
	DisableError   string `json:"disable_error,omitempty"`
}

func (h *Handler) adminGuard(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request)) {
	if r.Method != http.MethodGet && !mutating(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	u, err := h.users.Current(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	fn(w, r)
}

func (h *Handler) adminUsers(w http.ResponseWriter, _ *http.Request) {
	out := make([]adminUserView, 0)
	for _, u := range h.users.Users() {
		out = append(out, adminUserView{ID: u.ID, Username: u.Username, FullName: u.FullName, Admin: u.Admin, Disabled: u.Disabled, DisablePending: u.DisablePending, Deleting: u.Deleting, DeleteError: u.DeleteError, DisableError: u.DisableError})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (h *Handler) adminDisableUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID       string `json:"id"`
		Disabled bool   `json:"disabled"`
	}
	if !decodeBody(w, r, &b, 4096) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.accounts.SetDisabled(ctx, b.ID, b.Disabled); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "disabled": b.Disabled})
}

func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID              string `json:"id"`
		ConfirmUsername string `json:"confirm_username"`
	}
	if !decodeBody(w, r, &b, 4096) {
		return
	}
	u, ok := h.users.User(b.ID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
		return
	}
	if strings.TrimSpace(b.ConfirmUsername) != u.Username {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username confirmation did not match"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.accounts.Delete(ctx, b.ID); err != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "deleting", "error": "cleanup is pending", "account": b.ID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
