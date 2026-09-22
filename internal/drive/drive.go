package drive

import (
	"bytes"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/ratelimit"
	"github.com/bprendie/weazlcloud/internal/ready"
	"github.com/bprendie/weazlcloud/internal/users"
)

type Handler struct {
	users    *users.Store
	quota    *quota.Manager
	registry *filesvc.Registry
	mu       sync.Mutex
	locks    map[string]webdav.LockSystem
	limit    *ratelimit.Limiter
}
type resource struct {
	service *filesvc.Resource
	locks   webdav.LockSystem
}

func New() *Handler { return &Handler{} }
func NewMulti(us *users.Store) *Handler {
	return NewMultiWith(us, nil, filesvc.NewRegistry(us))
}
func NewMultiWith(us *users.Store, q *quota.Manager, registry *filesvc.Registry) *Handler {
	if registry == nil {
		registry = filesvc.NewRegistry(us)
	}
	return &Handler{users: us, quota: q, registry: registry, locks: make(map[string]webdav.LockSystem), limit: ratelimit.New(time.Minute, 8, 4096)}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	w.Header().Set("DAV", "1, 2")
	if r.URL.Path == "/live" && r.Method == http.MethodGet {
		ready.Live(w, r)
		return
	}
	if r.URL.Path == "/ready" && r.Method == http.MethodGet {
		ready.Serve(w, r)
		return
	}
	if forbidden(r.URL.Path) {
		http.Error(w, "weazlcloud: no desk", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, COPY, MOVE, PROPFIND, LOCK, UNLOCK")
		w.WriteHeader(http.StatusOK)
		return
	}
	if h.users == nil {
		challenge(w, "weazlcloud: drive locked")
		return
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		challenge(w, "weazlcloud: authentication required")
		return
	}
	remote := r.RemoteAddr
	if host, _, splitErr := net.SplitHostPort(remote); splitErr == nil {
		remote = host
	}
	key := username + ":" + remote
	if !h.limit.Allow(key) {
		w.Header().Set("Retry-After", "60")
		challenge(w, "weazlcloud: too many authentication attempts")
		return
	}
	u, err := h.users.Authenticate(username, password)
	if err != nil {
		challenge(w, "weazlcloud: authentication required")
		return
	}
	h.limit.Reset(key)
	ctx, release, ok := h.registry.Enter(r.Context(), u.ID)
	if !ok {
		challenge(w, "weazlcloud: authentication required")
		return
	}
	defer release()
	current, exists := h.users.User(u.ID)
	if !exists || current.Disabled || current.Deleting {
		challenge(w, "weazlcloud: authentication required")
		return
	}
	r = r.WithContext(ctx)
	service := h.registry.For(u)
	if err := service.Vault.UnlockNode(); err != nil {
		http.Error(w, "weazlcloud: vault unavailable", http.StatusServiceUnavailable)
		return
	}
	h.mu.Lock()
	locks := h.locks[u.ID]
	if locks == nil {
		locks = webdav.NewMemLS()
		h.locks[u.ID] = locks
	}
	h.mu.Unlock()
	dav := &webdav.Handler{FileSystem: &fileSystem{lib: service.Lib, userID: u.ID, users: h.users.Count(), quota: h.quota}, LockSystem: locks, Logger: func(req *http.Request, err error) {
		if err != nil {
			log.Printf("webdav %s %s: %v", req.Method, req.URL.Path, err)
		}
	}}
	if r.Method == "PROPFIND" && r.URL.Path == "/" {
		// GVfs compares the response href to the mount path. A relative root
		// href works whether the client supplied a trailing slash or not.
		rec := httptest.NewRecorder()
		dav.ServeHTTP(rec, r)
		body := bytes.Replace(rec.Body.Bytes(), []byte("<D:href>/</D:href>"), []byte("<D:href>.</D:href>"), 1)
		for k, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
		return
	}
	dav.ServeHTTP(w, r)
}

func challenge(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="weazl"`)
	http.Error(w, message, http.StatusUnauthorized)
}
func forbidden(path string) bool {
	switch path {
	case "/unlock", "/index.html", "/index.htm":
		return true
	}
	return strings.HasPrefix(path, "/desk")
}
