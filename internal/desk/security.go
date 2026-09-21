package desk

import (
	"net"
	"net/http"
	"strings"
)

func authKey(kind string, r *http.Request, identity string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return kind + ":" + host + ":" + strings.ToLower(strings.TrimSpace(identity))
}

func rateLimitResponse(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts; try again later"})
}
