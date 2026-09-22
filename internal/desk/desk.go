package desk

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/accountlifecycle"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/ratelimit"
	"github.com/bprendie/weazlcloud/internal/ready"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

//go:embed ui/*
var ui embed.FS

type Handler struct {
	files      http.Handler
	vault      *vault.Vault
	lib        *library.Library
	caps       *capsule.Store
	publicBase string
	driveBase  string
	placesPath string
	nodePath   string
	nodeMu     sync.RWMutex
	users      *users.Store
	quota      *quota.Manager
	registry   *filesvc.Registry
	authLimit  *ratelimit.Limiter
	changes    *filesvc.Hub
	uploads    *upload.Manager
	accounts   *accountlifecycle.Manager
}

func New(v *vault.Vault, lib *library.Library, caps *capsule.Store, publicBase, driveBase, placesPath string) *Handler {
	sub, err := fs.Sub(ui, "ui")
	if err != nil {
		panic(err)
	}
	grab, drive := loadPlaces(placesPath, publicBase, driveBase)
	changes := filesvc.NewHub()
	if lib != nil {
		lib.SetChangeSink(changes)
	}
	return &Handler{
		files: http.FileServer(http.FS(sub)), vault: v, lib: lib, caps: caps,
		publicBase: grab, driveBase: drive, placesPath: placesPath,
		authLimit: ratelimit.New(time.Minute, 8, 4096), changes: changes,
	}
}

func NewMulti(us *users.Store, caps *capsule.Store, q *quota.Manager, publicBase, driveBase, dataDir string, registries ...*filesvc.Registry) *Handler {
	h := New(nil, nil, caps, publicBase, driveBase, "")
	h.users, h.quota = us, q
	h.nodePath = filepath.Join(dataDir, "node.json")
	h.publicBase = loadNodeBase(h.nodePath, h.publicBase)
	if len(registries) > 0 && registries[0] != nil {
		h.registry = registries[0]
	} else {
		h.registry = filesvc.NewRegistry(us, q)
	}
	h.uploads = upload.New(filepath.Join(dataDir, "uploads"), func(u users.User) *filesvc.Resource {
		return h.registry.For(u)
	}, q, us.Count)
	h.accounts = accountlifecycle.New(us, h.registry, caps, h.uploads)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	if h.users != nil {
		h.serveMulti(w, r)
		return
	}
	switch {
	case r.URL.Path == "/live" && r.Method == http.MethodGet:
		ready.Live(w, r)
	case r.URL.Path == "/ready" && r.Method == http.MethodGet:
		ready.Serve(w, r)
	case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
		h.status(w)
	case r.URL.Path == "/api/forge" && r.Method == http.MethodPost:
		h.guard(w, r, h.forge)
	case r.URL.Path == "/api/unlock" && r.Method == http.MethodPost:
		h.guard(w, r, h.unlock)
	case r.URL.Path == "/api/lock" && r.Method == http.MethodPost:
		h.guard(w, r, h.lock)
	case r.URL.Path == "/api/kit" && r.Method == http.MethodPost:
		h.guard(w, r, h.kit)
	case r.URL.Path == "/api/places" && r.Method == http.MethodGet:
		h.getPlaces(w, r)
	case r.URL.Path == "/api/places" && r.Method == http.MethodPost:
		h.guard(w, r, h.savePlaces)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet && r.URL.Query().Get("path") == "":
		h.listLibrary(w, r)
	case r.URL.Path == "/api/library/thumbnail" && r.Method == http.MethodGet:
		h.thumbnailLibrary(w, r)
	case r.URL.Path == "/api/library/capability" && r.Method == http.MethodGet:
		h.capabilityLibrary(w, r)
	case r.URL.Path == "/api/library/events" && r.Method == http.MethodGet:
		h.libraryEvents(w, r)
	case r.URL.Path == "/api/library" && r.Method == http.MethodGet:
		h.getLibrary(w, r)
	case r.URL.Path == "/api/library" && r.Method == http.MethodPut:
		h.guard(w, r, h.putLibrary)
	case r.URL.Path == "/api/library" && r.Method == http.MethodDelete:
		h.guard(w, r, h.deleteLibrary)
	case r.URL.Path == "/api/library/copy" && r.Method == http.MethodPost:
		h.guard(w, r, h.copyLibrary)
	case r.URL.Path == "/api/trash" && r.Method == http.MethodGet:
		h.trash(w, r)
	case r.URL.Path == "/api/trash/restore" && r.Method == http.MethodPost:
		h.guard(w, r, h.restoreTrash)
	case r.URL.Path == "/api/trash" && r.Method == http.MethodDelete:
		h.guard(w, r, h.cleanupTrash)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodGet:
		h.listCapsules(w, r)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodPost:
		h.guard(w, r, h.mintCapsule)
	case r.URL.Path == "/api/capsules" && r.Method == http.MethodDelete:
		h.guard(w, r, h.revokeCapsule)
	default:
		h.files.ServeHTTP(w, r)
	}
}

func (h *Handler) ResumeDeletes(ctx context.Context) error {
	if h.accounts == nil {
		return nil
	}
	return h.accounts.ResumePending(ctx)
}

func (h *Handler) guard(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request)) {
	if !mutating(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	fn(w, r)
}
