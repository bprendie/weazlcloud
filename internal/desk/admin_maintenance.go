package desk

import "net/http"

func (h *Handler) adminMaintenance(w http.ResponseWriter, _ *http.Request) {
	if h.maintenanceStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "maintenance status unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": h.maintenanceStatus.Status()})
}
