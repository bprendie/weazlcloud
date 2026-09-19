package desk

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/ready"
	"github.com/bprendie/weazlcloud/internal/recovery"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type userResource struct {
	vault *vault.Vault
	lib   *library.Library
}

func (h *Handler) resource(u users.User) *userResource {
	h.resourceMu.Lock()
	defer h.resourceMu.Unlock()
	if res := h.resources[u.ID]; res != nil {
		return res
	}
	v := vault.New(h.users.VaultPath(u), h.users.NodeKeyPath(u))
	l := library.New(h.users.LibraryPath(u), h.users.CatalogPath(u), v)
	res := &userResource{vault: v, lib: l}
	h.resources[u.ID] = res
	return res
}

func (h *Handler) serveMulti(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/ready" && r.Method == http.MethodGet:
		readyServe(w, r)
	case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
		h.multiStatus(w, r)
	case r.URL.Path == "/api/bootstrap" && r.Method == http.MethodPost:
		h.multiGuard(w, r, false, h.bootstrap)
	case r.URL.Path == "/api/login" && r.Method == http.MethodPost:
		h.multiGuard(w, r, false, h.login)
	case r.URL.Path == "/api/access-requests" && r.Method == http.MethodPost:
		h.multiGuard(w, r, false, h.requestAccess)
	case r.URL.Path == "/api/access-requests" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.listAccessRequests)
	case r.URL.Path == "/api/access-requests/approve" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.approveAccess)
	case r.URL.Path == "/api/access-requests/reject" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.rejectAccess)
	case r.URL.Path == "/api/account-setup" && r.Method == http.MethodPost:
		h.multiGuard(w, r, false, h.completeAccess)
	case r.URL.Path == "/api/logout" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.logout)
	case r.URL.Path == "/api/users" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.createUser)
	case r.URL.Path == "/api/me" && r.Method == http.MethodGet:
		h.me(w, r)
	case r.URL.Path == "/api/settings" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.saveSettings)
	case r.URL.Path == "/api/vault/rekey" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.rekeyVault)
	case r.URL.Path == "/api/unlock" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiUnlock)
	case r.URL.Path == "/api/lock" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiLock)
	case r.URL.Path == "/api/kit" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiKit)
	case r.URL.Path == "/api/quota" && r.Method == http.MethodGet:
		h.multiQuota(w, r)
	case r.URL.Path == "/api/qr" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.qr)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet && r.URL.Query().Get("path") == "":
		h.multiGuard(w, r, true, h.multiListLibrary)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiGetLibrary)
	case r.URL.Path == "/api/library" && r.Method == http.MethodPut:
		h.multiGuard(w, r, true, h.multiPutLibrary)
	case r.URL.Path == "/api/library/folder" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiCreateFolder)
	case r.URL.Path == "/api/library/rename" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiRename)
	case r.URL.Path == "/api/library" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.multiDeleteLibrary)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiListCapsules)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiMintCapsule)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.multiRevokeCapsule)
	case r.URL.Path == "/api/places" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiGetPlaces)
	case r.URL.Path == "/api/places" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiSavePlaces)
	case r.URL.Path == "/api/node" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.getNodeSettings)
	case r.URL.Path == "/api/node" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.saveNodeSettings)
	default:
		h.files.ServeHTTP(w, r)
	}
}

func readyServe(w http.ResponseWriter, r *http.Request) { ready.Serve(w, r) }

func (h *Handler) multiGuard(w http.ResponseWriter, r *http.Request, auth bool, fn func(http.ResponseWriter, *http.Request)) {
	if r.Method != http.MethodGet && !mutating(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	if auth {
		if _, _, err := h.currentResource(r); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
	}
	fn(w, r)
}

type bootstrapBody struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	VaultPassphrase string `json:"vault_passphrase"`
	Confirm         string `json:"confirm"`
}
type loginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type accessRequestBody struct {
	Username string `json:"username"`
	Note     string `json:"note"`
}
type accessDecisionBody struct {
	ID string `json:"id"`
}
type accountSetupBody struct {
	ID              string `json:"id"`
	Token           string `json:"token"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	VaultPassphrase string `json:"vault_passphrase"`
	Confirm         string `json:"confirm"`
}
type settingsBody struct {
	FullName        string `json:"full_name"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
type rekeyBody struct {
	Current string `json:"current"`
	Next    string `json:"next"`
	Confirm string `json:"confirm"`
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return false
	}
	return true
}

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
	users.SetSession(w, token)
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
	u, err := h.users.Authenticate(b.Username, b.Password)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	token, err := h.users.Login(u)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	users.SetSession(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username, "admin": u.Admin})
}

func (h *Handler) requestAccess(w http.ResponseWriter, r *http.Request) {
	var b accessRequestBody
	if !decodeBody(w, r, &b, 8192) {
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

func (h *Handler) currentResource(r *http.Request) (*userResource, users.User, error) {
	u, err := h.users.Current(r)
	if err != nil {
		return nil, users.User{}, err
	}
	res := h.resource(u)
	return res, u, nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username, "full_name": u.FullName, "admin": u.Admin, "unlocked": h.resource(u).vault.Unlocked()})
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
	if err := res.vault.Rekey([]byte(b.Current), []byte(b.Next), []byte(b.Confirm)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "vault rekeyed"})
}

func (h *Handler) multiStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"setup": h.users.Count() > 0, "users": h.users.Count(), "authenticated": false}
	if u, err := h.users.Current(r); err == nil {
		out["authenticated"] = true
		out["username"] = u.Username
		out["admin"] = u.Admin
		out["unlocked"] = h.resource(u).vault.Unlocked()
	}
	if h.quota != nil {
		if q, err := h.quota.Status(h.users.Count()); err == nil {
			out["quota"] = q
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) multiUnlock(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var b passBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := res.vault.Unlock([]byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "unlocked"})
}

func (h *Handler) multiLock(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	res.vault.Lock()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "locked"})
}

func (h *Handler) multiKit(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var b passBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := res.vault.Check([]byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	out := filepath.Join(filepath.Dir(res.vault.Path()), "weazlcloud-recovery.wzck")
	cfg, _ := json.Marshal(map[string]string{"format": "weazlcloud-places"})
	if err := recovery.Export(out, res.vault.Path(), cfg, []byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	if _, err := recovery.Verify(out, []byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "kit"})
}

func (h *Handler) multiQuota(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if h.quota == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "quota unavailable"})
		return
	}
	q, err := h.quota.Status(h.users.Count())
	if err != nil {
		apiUsersError(w, err)
		return
	}
	out := map[string]any{"capacity": q.Capacity, "used": q.Used, "limit": q.Limit, "percent": q.Percent, "users": q.Users}
	if res.vault.Unlocked() {
		if dedupe, logical, unique, e := res.lib.Dedupe(r.Context()); e == nil {
			out["dedupe_percent"] = dedupe
			out["logical_bytes"] = logical
			out["unique_bytes"] = unique
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) multiListLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h2 := *h
	h2.vault, h2.lib = res.vault, res.lib
	h2.listLibrary(w, r)
}
func (h *Handler) multiGetLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h2 := *h
	h2.vault, h2.lib = res.vault, res.lib
	h2.getLibrary(w, r)
}
func (h *Handler) multiDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h2 := *h
	h2.vault, h2.lib = res.vault, res.lib
	h2.deleteLibrary(w, r)
}

func (h *Handler) multiPutLibrary(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	path := r.URL.Query().Get("path")
	if r.ContentLength < 0 {
		http.Error(w, `{"error":"content length required for quota reservation"}`, http.StatusLengthRequired)
		return
	}
	if h.quota != nil {
		current := int64(0)
		if old, e := res.lib.Get(r.Context(), path); e == nil {
			current = int64(len(old))
		}
		used, e := res.lib.Usage(r.Context())
		if e != nil {
			apiError(w, e)
			return
		}
		release, err := h.quota.Reserve(u.ID, h.users.Count(), used, current, r.ContentLength)
		if err != nil {
			apiError(w, err)
			return
		}
		defer release()
	}
	f, err := res.lib.PutReader(r.Context(), path, r.Body, r.ContentLength)
	if err != nil {
		apiError(w, err)
		return
	}
	_ = u
	writeJSON(w, http.StatusOK, fileView{Path: f.Path, Size: f.Size, Mtime: f.Mtime})
}

func (h *Handler) multiCreateFolder(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.lib.Mkdir(r.Context(), body.Path); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": body.Path, "folder": true})
}

func (h *Handler) multiRename(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.lib.Rename(r.Context(), body.From, body.To); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "renamed"})
}

func (h *Handler) multiListCapsules(w http.ResponseWriter, r *http.Request) {
	_, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	out := []map[string]any{}
	for _, rec := range h.caps.ListOwner(u.ID) {
		out = append(out, rec.View())
	}
	writeJSON(w, http.StatusOK, map[string]any{"capsules": out, "base": h.publicBase})
}

func (h *Handler) multiMintCapsule(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h2 := *h
	h2.vault, h2.lib = res.vault, res.lib
	var b mintBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	payload, rec, err := h2.seal(r, b)
	if err != nil {
		apiError(w, err)
		return
	}
	rec.Owner = u.ID
	got, err := h.caps.Mint(rec, b.Passphrase, payload)
	if err != nil {
		apiError(w, err)
		return
	}
	view := got.View()
	view["url"] = strings.TrimRight(h.publicBase, "/") + "/g/" + got.ID
	writeJSON(w, http.StatusOK, view)
}
func (h *Handler) multiRevokeCapsule(w http.ResponseWriter, r *http.Request) {
	_, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"missing id"}`, 400)
		return
	}
	if err := h.caps.RevokeOwner(id, u.ID); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "revoked"})
}

func (h *Handler) multiGetPlaces(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h2 := *h
	h2.vault, h2.placesPath = res.vault, h.users.PlacesPath(u)
	h2.getPlaces(w, r)
}
func (h *Handler) multiSavePlaces(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h2 := *h
	h2.vault, h2.placesPath = res.vault, h.users.PlacesPath(u)
	h2.savePlaces(w, r)
}

func (h *Handler) getNodeSettings(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": strings.TrimPrefix(h.publicBase, "https://")})
}

func (h *Handler) saveNodeSettings(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	host := strings.TrimPrefix(strings.TrimSpace(body.Hostname), "https://")
	if host == "" || strings.ContainsAny(host, "/?#@") || strings.ContainsAny(host, " \t\r\n") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hostname must be a host only, without https://, a path, or credentials"})
		return
	}
	if err := h.saveNodeBase("https://" + host); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": host})
}

func apiUsersError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	if err == users.ErrUserExists || err == users.ErrBadUsername {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
