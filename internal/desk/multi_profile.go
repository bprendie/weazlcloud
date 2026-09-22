package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (h *Handler) currentResource(r *http.Request) (*filesvc.Resource, users.User, error) {
	u, err := h.users.Current(r)
	if err != nil {
		return nil, users.User{}, err
	}
	res := h.registry.For(u)
	return res, u, nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username, "full_name": u.FullName, "admin": u.Admin, "unlocked": h.registry.For(u).Vault.Unlocked()})
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var b settingsBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if b.NewPassword != "" {
		if err := h.users.ChangePassword(u.ID, b.CurrentPassword, b.NewPassword); err != nil {
			apiUsersError(w, err)
			return
		}
	}
	u, err = h.users.UpdateProfile(u.ID, b.FullName)
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username, "full_name": u.FullName})
}

func (h *Handler) rekeyVault(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var b rekeyBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := res.Vault.Rekey([]byte(b.Current), []byte(b.Next), []byte(b.Confirm)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "vault rekeyed"})
}

func (h *Handler) multiStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"setup": h.users.Count() > 0, "users": h.users.Count(), "authenticated": false}
	if u, err := h.users.Current(r); err == nil {
		ctx, release, ok := h.registry.Enter(r.Context(), u.ID)
		if !ok { writeJSON(w, http.StatusOK, out); return }
		defer release()
		r = r.WithContext(ctx)
		current, exists := h.users.User(u.ID)
		if !exists || current.Disabled || current.Deleting { writeJSON(w, http.StatusOK, out); return }
		out["authenticated"] = true
		out["username"] = u.Username
		out["admin"] = u.Admin
		out["unlocked"] = h.registry.For(u).Vault.Unlocked()
	}
	if h.quota != nil {
		if q, err := h.quota.Status(h.users.Count()); err == nil {
			out["quota"] = q
		}
	}
	writeJSON(w, http.StatusOK, out)
}
