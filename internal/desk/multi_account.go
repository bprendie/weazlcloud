package desk

import (
	"net/http"
	"os"

	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if h.users.Count() != 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "bootstrap already complete"})
		return
	}
	var b bootstrapBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if b.VaultPassphrase == "" {
		b.VaultPassphrase = b.Password
	}
	legacy := vault.New(h.users.LegacyVaultPath(), h.users.LegacyNodeKeyPath())
	hasLegacy := legacy.Exists()
	if hasLegacy {
		if err := legacy.Check([]byte(b.VaultPassphrase)); err != nil {
			apiError(w, err)
			return
		}
	}
	u, err := h.users.Create(b.Username, b.Password, true)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if hasLegacy {
		if err := migrateFile(h.users.LegacyVaultPath(), h.users.VaultPath(u)); err != nil {
			apiError(w, err)
			return
		}
		if err := migrateFile(h.users.LegacyNodeKeyPath(), h.users.NodeKeyPath(u)); err != nil {
			apiError(w, err)
			return
		}
		_ = migrateFile(h.users.LegacyCatalogPath(), h.users.CatalogPath(u))
		_ = migrateFile(h.users.LegacyPlacesPath(), h.users.PlacesPath(u))
		_ = migrateDir(h.users.LegacyLibraryPath(), h.users.LibraryPath(u))
		if err := h.caps.AssignOwner(u.ID); err != nil {
			apiError(w, err)
			return
		}
	} else {
		v := vault.New(h.users.VaultPath(u), h.users.NodeKeyPath(u))
		confirm := b.Confirm
		if confirm == "" {
			confirm = b.VaultPassphrase
		}
		if err := v.Forge([]byte(b.VaultPassphrase), []byte(confirm)); err != nil {
			apiError(w, err)
			return
		}
	}
	token, err := h.users.Login(u)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h.users.SetSession(w, token)
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "username": u.Username, "admin": u.Admin})
}

func migrateFile(old, new string) error {
	if _, err := os.Stat(old); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(old, new)
}

func migrateDir(old, new string) error {
	if _, err := os.Stat(old); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(old, new)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var b loginBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	key := authKey("login", r, b.Username)
	if !h.authLimit.Allow(key) {
		rateLimitResponse(w)
		return
	}
	u, err := h.users.Authenticate(b.Username, b.Password)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h.authLimit.Reset(key)
	token, err := h.users.Login(u)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h.users.SetSession(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username, "admin": u.Admin})
}

func (h *Handler) requestAccess(w http.ResponseWriter, r *http.Request) {
	var b accessRequestBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	key := authKey("access", r, b.Username)
	if !h.authLimit.Allow(key) {
		rateLimitResponse(w)
		return
	}
	q, err := h.users.RequestAccess(b.Username, b.Note)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": q.ID, "username": q.Username, "status": q.Status, "created_at": q.CreatedAt})
}

func (h *Handler) listAccessRequests(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	requests := h.users.AccessRequests()
	public := make([]map[string]any, 0, len(requests))
	for _, q := range requests {
		public = append(public, map[string]any{"id": q.ID, "username": q.Username, "note": q.Note, "status": q.Status, "created_at": q.CreatedAt, "approved_at": q.ApprovedAt, "consumed_at": q.ConsumedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": public})
}

func (h *Handler) approveAccess(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	var b accessDecisionBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	q, token, err := h.users.ApproveAccess(b.ID)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": q, "setup_token": token})
}

func (h *Handler) rejectAccess(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	var b accessDecisionBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := h.users.RejectAccess(b.ID); err != nil {
		apiUsersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "rejected"})
}

func (h *Handler) completeAccess(w http.ResponseWriter, r *http.Request) {
	var b accountSetupBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	u, err := h.users.CompleteAccess(b.ID, b.Token, b.Username, b.Password)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	pass := b.VaultPassphrase
	if pass == "" {
		pass = b.Password
	}
	confirm := b.Confirm
	if confirm == "" {
		confirm = pass
	}
	v := vault.New(h.users.VaultPath(u), h.users.NodeKeyPath(u))
	if err := v.Forge([]byte(pass), []byte(confirm)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "username": u.Username, "admin": u.Admin})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	admin, err := h.users.Current(r)
	if err != nil || !admin.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	var b bootstrapBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if b.VaultPassphrase == "" {
		b.VaultPassphrase = b.Password
	}
	u, err := h.users.Create(b.Username, b.Password, false)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	v := vault.New(h.users.VaultPath(u), h.users.NodeKeyPath(u))
	confirm := b.Confirm
	if confirm == "" {
		confirm = b.VaultPassphrase
	}
	if err := v.Forge([]byte(b.VaultPassphrase), []byte(confirm)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "username": u.Username, "admin": u.Admin})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.users.Logout(r)
	users.ClearSession(w)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "logged out"})
}
