package drive

import (
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/ready"
)

type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	if r.URL.Path == "/ready" && r.Method == http.MethodGet {
		ready.Serve(w, r)
		return
	}
	if forbidden(r.URL.Path) {
		http.Error(w, "weazlcloud: no desk", http.StatusNotFound)
		return
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="weazl"`)
	http.Error(w, "weazlcloud: drive locked", http.StatusUnauthorized)
}

func forbidden(path string) bool {
	switch path {
	case "/unlock", "/index.html", "/index.htm":
		return true
	}
	return strings.HasPrefix(path, "/desk")
}
