package drive

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/ready"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type Handler struct {
	users     *users.Store
	mu        sync.Mutex
	resources map[string]*resource
	locks     webdav.LockSystem
}
type resource struct {
	vault *vault.Vault
	lib   *library.Library
}

func New() *Handler { return &Handler{} }
func NewMulti(us *users.Store) *Handler {
	return &Handler{users: us, resources: make(map[string]*resource), locks: webdav.NewMemLS()}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	w.Header().Set("DAV", "1, 2")
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
	u, err := h.users.Authenticate(username, password)
	if err != nil {
		challenge(w, "weazlcloud: authentication required")
		return
	}
	h.mu.Lock()
	res := h.resources[u.ID]
	if res == nil {
		v := vault.New(h.users.VaultPath(u), h.users.NodeKeyPath(u))
		res = &resource{vault: v, lib: library.New(h.users.LibraryPath(u), h.users.CatalogPath(u), v)}
		h.resources[u.ID] = res
	}
	h.mu.Unlock()
	if err := res.vault.UnlockNode(); err != nil {
		http.Error(w, "weazlcloud: vault unavailable", http.StatusServiceUnavailable)
		return
	}
	dav := &webdav.Handler{FileSystem: &fileSystem{lib: res.lib}, LockSystem: h.locks}
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

type fileSystem struct{ lib *library.Library }

func clean(name string) (string, error) {
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", nil
	}
	p := filepath.ToSlash(filepath.Clean(name))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "\x00") {
		return "", os.ErrInvalid
	}
	return p, nil
}
func (f *fileSystem) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	name, err := clean(name)
	if err != nil {
		return err
	}
	if name == "" {
		return os.ErrExist
	}
	return mapError(f.lib.Mkdir(ctx, name))
}
func (f *fileSystem) RemoveAll(_ context.Context, name string) error {
	name, err := clean(name)
	if err != nil {
		return err
	}
	if name == "" {
		return os.ErrPermission
	}
	return mapError(f.lib.Delete(name))
}
func (f *fileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldName, err := clean(oldName)
	if err != nil {
		return err
	}
	newName, err = clean(newName)
	if err != nil {
		return err
	}
	return mapError(f.lib.Rename(ctx, oldName, newName))
}

func (f *fileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	name, err := clean(name)
	if err != nil {
		return nil, err
	}
	if err := f.lib.Ensure(ctx); err != nil {
		return nil, mapError(err)
	}
	if name == "" {
		return info{name: "/", dir: true, mode: os.ModeDir | 0o755, mod: time.Unix(0, 0).UTC()}, nil
	}
	for _, row := range f.lib.List() {
		if row.Path == name {
			return info{name: filepath.Base(name), size: row.Size, dir: row.Folder, mode: fileMode(row.Folder), mod: row.Mtime}, nil
		}
	}
	return nil, os.ErrNotExist
}

func (f *fileSystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	name, err := clean(name)
	if err != nil {
		return nil, err
	}
	if err := f.lib.Ensure(ctx); err != nil {
		return nil, mapError(err)
	}
	st, statErr := f.Stat(ctx, name)
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if statErr == nil && st.IsDir() {
		return &davFile{info: st, dir: f.directory(name)}, nil
	}
	if !write && os.IsNotExist(statErr) {
		return nil, os.ErrNotExist
	}
	tmp, err := os.CreateTemp("", "weazl-dav-*")
	if err != nil {
		return nil, err
	}
	d := &davFile{file: tmp, name: name, lib: f.lib, write: write, info: info{name: filepath.Base(name), mode: 0o600, mod: time.Now().UTC()}}
	if !write {
		if err := f.lib.WriteTo(ctx, name, tmp); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, mapError(err)
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, err
		}
		d.info = info{name: filepath.Base(name), size: st.Size(), mode: 0o600, mod: st.ModTime()}
	}
	return d, nil
}
func (f *fileSystem) directory(name string) []os.FileInfo {
	prefix := name
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]bool{}
	out := []os.FileInfo{}
	for _, row := range f.lib.List() {
		if !strings.HasPrefix(row.Path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(row.Path, prefix)
		part := strings.SplitN(rest, "/", 2)[0]
		if seen[part] {
			continue
		}
		seen[part] = true
		isDir := strings.Contains(rest, "/") || row.Folder
		out = append(out, info{name: part, size: row.Size, dir: isDir, mode: fileMode(isDir), mod: row.Mtime})
	}
	return out
}

type davFile struct {
	file   *os.File
	lib    *library.Library
	name   string
	write  bool
	info   os.FileInfo
	dir    []os.FileInfo
	closed bool
}

func (d *davFile) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	if d.file == nil {
		return nil
	}
	defer os.Remove(d.file.Name())
	if d.write {
		if _, err := d.file.Seek(0, io.SeekStart); err != nil {
			d.file.Close()
			return err
		}
		if _, err := d.lib.PutReader(context.Background(), d.name, d.file, -1); err != nil {
			d.file.Close()
			return mapError(err)
		}
	}
	return d.file.Close()
}
func (d *davFile) Read(p []byte) (int, error) { return d.file.Read(p) }
func (d *davFile) Write(p []byte) (int, error) {
	if !d.write {
		return 0, os.ErrPermission
	}
	return d.file.Write(p)
}
func (d *davFile) Seek(o int64, whence int) (int64, error) { return d.file.Seek(o, whence) }
func (d *davFile) Stat() (os.FileInfo, error)              { return d.info, nil }
func (d *davFile) Readdir(count int) ([]os.FileInfo, error) {
	if d.dir == nil {
		return nil, os.ErrInvalid
	}
	if count <= 0 {
		out := d.dir
		d.dir = nil
		return out, nil
	}
	if len(d.dir) == 0 {
		return nil, io.EOF
	}
	n := count
	if n > len(d.dir) {
		n = len(d.dir)
	}
	out := d.dir[:n]
	d.dir = d.dir[n:]
	return out, nil
}

type info struct {
	name string
	size int64
	dir  bool
	mode os.FileMode
	mod  time.Time
}

func (i info) Name() string       { return i.name }
func (i info) Size() int64        { return i.size }
func (i info) Mode() os.FileMode  { return i.mode }
func (i info) ModTime() time.Time { return i.mod }
func (i info) IsDir() bool        { return i.dir }
func (i info) Sys() any           { return nil }
func fileMode(dir bool) os.FileMode {
	if dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "not in the library") {
		return os.ErrNotExist
	}
	return err
}
