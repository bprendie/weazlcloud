package desk

import (
	"net/http"
	"strings"
)

// Omitted negotiation preserves legacy v1 clients. A future contract must not
// silently fall back to v1 mutation semantics. Header/query must agree when both
// are present. This is independent of encrypted on-disk schema versions.
func (h *Handler) rejectMobileContract(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") && r.URL.Path != "/.well-known/weazlcloud" {
		return false
	}
	query, present := r.URL.Query()["contract_version"]
	headers := r.Header.Values("X-Weazl-Mobile-Contract")
	if (!present || len(query) == 1 && query[0] == "1") && (len(headers) == 0 || len(headers) == 1 && headers[0] == "1") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 400, map[string]any{"error": "unsupported mobile contract version", "code": "unsupported_contract_version", "supported_contract_versions": []int{1}})
	return true
}
