package ready

import (
	"encoding/json"
	"net/http"
)

func Live(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, map[string]any{"ok": true, "process": "live"})
}

func Serve(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func Storage(w http.ResponseWriter, r *http.Request, check func() error) {
	if err := check(); err != nil {
		write(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "storage": "unavailable", "error": "data volume is unavailable"})
		return
	}
	write(w, http.StatusOK, map[string]any{"ok": true, "storage": "ready"})
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
