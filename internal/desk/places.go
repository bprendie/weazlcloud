package desk

import (
	"encoding/json"
	"fmt"
	"net"
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
	h.getPlacesForBase(w, v, h.grabBase(), h.driveBase)
}

func (h *Handler) getPlacesForBase(w http.ResponseWriter, v *vault.Vault, grab, drive string) {
	p := places{Grab: grab, Drive: drive}
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
		p.Grab = h.grabBase()
	} else {
		h.publicBase = p.Grab
		h.driveBase = p.Drive
	}
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
	b, err := json.MarshalIndent(struct {
		GrabBase string `json:"grab_base"`
	}{base}, "", "  ")
	if err != nil {
		return err
	}
	if h.nodePath != "" {
		if err := cryptox.AtomicWrite(h.nodePath, append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	h.nodeMu.Lock()
	h.publicBase = base
	h.nodeMu.Unlock()
	return nil
}

func (h *Handler) grabBase() string {
	h.nodeMu.RLock()
	defer h.nodeMu.RUnlock()
	return h.publicBase
}

func validateHostname(host string) error {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return fmt.Errorf("hostname must be a DNS host")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("hostname contains an invalid label")
		}
		for _, r := range label {
			if !(r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return fmt.Errorf("hostname contains an invalid character")
			}
		}
	}
	return nil
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
