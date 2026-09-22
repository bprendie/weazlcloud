package desk

import (
	"net/http"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) multiListCapsules(w http.ResponseWriter, r *http.Request) {
	_, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	_, _ = h.caps.CleanupExpired(time.Now().UTC())
	out := []map[string]any{}
	for _, rec := range h.caps.ListOwner(u.ID) {
		out = append(out, rec.View())
	}
	writeJSON(w, http.StatusOK, map[string]any{"capsules": out, "base": h.grabBase()})
}

func (h *Handler) multiMintCapsule(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	if strings.TrimSpace(h.grabBase()) == "" || !strings.HasPrefix(h.grabBase(), "https://") {
		apiError(w, capsule.ErrNeedBase)
		return
	}
	var b mintBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	rec, source, err := h.sealFor(r, b, res.Vault, res.Lib)
	if err != nil {
		apiError(w, err)
		return
	}
	rec.Owner = u.ID
	var release func()
	if h.quota != nil {
		used, e := res.Lib.Usage(r.Context())
		if e != nil {
			apiError(w, e)
			return
		}
		release, err = h.quota.Reserve(u.ID, h.users.Count(), used, 0, rec.Size)
		if err != nil {
			apiError(w, err)
			return
		}
		defer release()
	}
	got, err := h.caps.MintStream(rec, b.Passphrase, source)
	if err != nil {
		apiError(w, err)
		return
	}
	view := got.View()
	view["url"] = strings.TrimRight(h.grabBase(), "/") + "/g/" + got.ID
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
	grab, drive := loadPlaces(h.users.PlacesPath(u), h.grabBase(), h.driveBase)
	h.getPlacesForBase(w, res.Vault, grab, drive)
}
func (h *Handler) multiSavePlaces(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h.savePlacesFor(w, r, res.Vault, h.users.PlacesPath(u))
}

func (h *Handler) getNodeSettings(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Current(r)
	if err != nil || !u.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": strings.TrimPrefix(h.grabBase(), "https://")})
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
	if err := validateHostname(host); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hostname must be a valid host only, without https://, a path, or credentials"})
		return
	}
	if err := h.saveNodeBase("https://" + host); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": host})
}
