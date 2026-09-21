package desk

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type places struct {
	Grab  string `json:"grab"`
	Drive string `json:"drive"`
	Token string `json:"token,omitempty"`
}

func (h *Handler) getPlaces(w http.ResponseWriter, _ *http.Request) {
	h.getPlacesFor(w, h.vault)
}

func (h *Handler) getPlacesFor(w http.ResponseWriter, v *vault.Vault) {
	p := places{Grab: h.publicBase, Drive: h.driveBase}
	if v != nil && v.Unlocked() {
		if t, err := v.DriveToken(); err == nil {
			p.Token = t
		}
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) savePlaces(w http.ResponseWriter, r *http.Request) {
	h.savePlacesFor(w, r, h.vault, h.placesPath)
}

func (h *Handler) savePlacesFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, placesPath string) {
	var p places
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	p.Grab = strings.TrimSpace(p.Grab)
	p.Drive = strings.TrimSpace(p.Drive)
	if p.Grab != "" && !strings.HasPrefix(p.Grab, "https://") {
		http.Error(w, `{"error":"grab base must be https://"}`, http.StatusBadRequest)
		return
	}
	if h.users != nil {
		p.Grab = h.publicBase
	}
	h.publicBase = p.Grab
	h.driveBase = p.Drive
	if placesPath != "" {
		b, _ := json.MarshalIndent(p, "", "  ")
		_ = cryptox.AtomicWrite(placesPath, append(b, '\n'), 0o600)
	}
	writeJSON(w, http.StatusOK, p)
}

func loadNodeBase(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var n struct {
		GrabBase string `json:"grab_base"`
	}
	if json.Unmarshal(b, &n) != nil || strings.TrimSpace(n.GrabBase) == "" {
		return fallback
	}
	return strings.TrimSpace(n.GrabBase)
}

func (h *Handler) saveNodeBase(base string) error {
	h.publicBase = base
	if h.nodePath == "" {
		return nil
	}
	b, err := json.MarshalIndent(struct {
		GrabBase string `json:"grab_base"`
	}{base}, "", "  ")
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(h.nodePath, append(b, '\n'), 0o600)
}

func loadPlaces(path, grab, drive string) (string, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return grab, drive
	}
	var p places
	if json.Unmarshal(b, &p) != nil {
		return grab, drive
	}
	if strings.TrimSpace(p.Grab) != "" {
		grab = p.Grab
	}
	if strings.TrimSpace(p.Drive) != "" {
		drive = p.Drive
	}
	return grab, drive
}

func placesFile(dataDir string) string { return filepath.Join(dataDir, "places.json") }
