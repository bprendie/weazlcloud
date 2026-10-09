package desk

import "net/http"

// Existing collection/grab helpers rely on caller scope, CSRF and drain guards.
func (h *Handler) tryGuardedMobileRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/v1/photos/source-collections/lookup", "/api/v1/photos/collections", "/api/v1/photos/source-collections", "/api/v1/photos/source-memberships":
		h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) { h.tryMobileCollections(w, r) })
		return true
	case "/api/v1/grabs/operations":
		if r.Method != http.MethodGet {
			mobileMethodError(w)
			return true
		}
	case "/api/capsules", "/api/v1/photos/grabs":
		if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") == "" {
			return false
		}
	default:
		return false
	}
	h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) { h.tryMobileGrabs(w, r) })
	return true
}
