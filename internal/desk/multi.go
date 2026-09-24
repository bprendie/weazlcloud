package desk

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/ready"
)

func (h *Handler) serveMulti(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/live" && r.Method == http.MethodGet:
		ready.Live(w, r)
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
	case r.URL.Path == "/api/admin/users" && r.Method == http.MethodGet:
		h.adminGuard(w, r, h.adminUsers)
	case r.URL.Path == "/api/admin/maintenance" && r.Method == http.MethodGet:
		h.adminGuard(w, r, h.adminMaintenance)
	case r.URL.Path == "/api/admin/users/disable" && r.Method == http.MethodPost:
		h.adminGuard(w, r, h.adminDisableUser)
	case r.URL.Path == "/api/admin/users/delete" && r.Method == http.MethodPost:
		h.adminGuard(w, r, h.adminDeleteUser)
	case r.URL.Path == "/api/me" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.me)
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
		h.multiGuard(w, r, true, h.multiQuota)
	case r.URL.Path == "/api/qr" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.qr)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet && r.URL.Query().Get("path") == "":
		h.multiGuard(w, r, true, h.multiListLibrary)
	case r.URL.Path == "/api/library/thumbnail" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiThumbnailLibrary)
	case r.URL.Path == "/api/library/capability" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiCapabilityLibrary)
	case r.URL.Path == "/api/library/events" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiLibraryEvents)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiGetLibrary)
	case r.URL.Path == "/api/library" && r.Method == http.MethodPut:
		h.multiGuard(w, r, true, h.multiPutLibrary)
	case r.URL.Path == "/api/uploads" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiCreateUpload)
	case r.URL.Path == "/api/uploads" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiListUploads)
	case strings.HasPrefix(r.URL.Path, "/api/uploads/"):
		h.multiGuard(w, r, true, h.multiUploadRoute)
	case r.URL.Path == "/api/library/folder" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiCreateFolder)
	case r.URL.Path == "/api/library/rename" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiRename)
	case r.URL.Path == "/api/library/copy" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiCopy)
	case r.URL.Path == "/api/library" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.multiDeleteLibrary)
	case r.URL.Path == "/api/trash" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiTrash)
	case r.URL.Path == "/api/trash/restore" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiRestoreTrash)
	case r.URL.Path == "/api/trash" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.multiCleanupTrash)
	case r.URL.Path == "/api/library/archive" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.multiCreateArchive)
	case r.URL.Path == "/api/library/archive" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.multiArchive)
	case r.URL.Path == "/api/library/archive" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.multiCancelArchive)
	case r.URL.Path == "/api/takeout" && r.Method == http.MethodGet:
		h.multiGuard(w, r, true, h.listTakeout)
	case r.URL.Path == "/api/takeout" && r.Method == http.MethodPost:
		h.multiGuard(w, r, true, h.startTakeout)
	case r.URL.Path == "/api/takeout" && r.Method == http.MethodDelete:
		h.multiGuard(w, r, true, h.cancelTakeout)
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
		u, err := h.users.Current(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		ctx, release, ok := h.registry.Enter(r.Context(), u.ID)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "account unavailable"})
			return
		}
		defer release()
		r = r.WithContext(ctx)
		current, ok := h.users.User(u.ID)
		if !ok || current.Disabled || current.Deleting {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "account unavailable"})
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
