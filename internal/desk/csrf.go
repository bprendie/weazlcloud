package desk

import (
	"net/http"
	"net/url"
)

func mutating(r *http.Request) bool {
	if r.Header.Get("X-Weazl-Desk") != "1" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
