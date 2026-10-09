package users

import (
	"errors"
	"net/http"
	"sort"
	"strings"
)

const (
	PhotosRead   = "photos:read"
	PhotosWrite  = "photos:write"
	FilesRead    = "files:read"
	FilesWrite   = "files:write"
	BackupWrite  = "backup:write"
	GrabsRead    = "grabs:read"
	GrabsWrite   = "grabs:write"
	StorageRead  = "storage:read"
	LegacyPhotos = "photos:v1"
)

var ErrInsufficientScope = errors.New("insufficient scope")

func normalizeScopes(scopes []string) ([]string, error) {
	if scopes == nil {
		return nil, nil
	}
	out := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, v := range scopes {
		switch v {
		case PhotosRead, PhotosWrite, FilesRead, FilesWrite, BackupWrite, GrabsRead, GrabsWrite, StorageRead:
		default:
			return nil, errors.New("unsupported device scope")
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	if seen[BackupWrite] && !seen[FilesWrite] {
		return nil, errors.New("backup:write requires files:write")
	}
	sort.Strings(out)
	return out, nil
}

// GrantedScopes returns a detached explicit projection, including legacy identity.
func (d Device) GrantedScopes() []string {
	if d.Scopes == nil {
		return []string{LegacyPhotos}
	}
	return append([]string{}, d.Scopes...)
}
func (d Device) HasScopes(scopes ...string) bool {
	for _, want := range scopes {
		found := false
		for _, got := range d.GrantedScopes() {
			if got == want {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type routeGrant struct {
	method, path string
	scopes       []string
	legacy       bool
}

func grant(method, path string, legacy bool, scopes ...string) routeGrant {
	return routeGrant{method, path, scopes, legacy}
}

// Every route includes its method. Wildcards match exactly one nonempty segment.
var deviceGrants = []routeGrant{
	grant("POST", "/api/v1/photos/lookup", true, PhotosRead),
	grant("GET", "/api/v1/photos", true, PhotosRead),
	grant("GET", "/api/v1/photos/capabilities", true, PhotosRead),
	grant("GET", "/api/v1/photos/albums", true, PhotosRead),
	grant("GET", "/api/v1/photos/albums/memberships", true, PhotosRead),
	grant("POST", "/api/v1/photos/albums", true, PhotosWrite),
	grant("GET", "/api/v1/photos/search", true, PhotosRead),
	grant("GET", "/api/v1/photos/dates", true, PhotosRead),
	grant("GET", "/api/v1/photos/seek", true, PhotosRead),
	grant("GET", "/api/v1/photos/sync", true, PhotosRead),
	grant("POST", "/api/v1/photos/sync/checkpoint", true, PhotosRead),
	grant("GET", "/api/v1/photos/assets/*", true, PhotosRead),
	grant("POST", "/api/v1/photos/assets/*", true, PhotosWrite),
	grant("GET", "/api/v1/photos/assets/*/thumbnail", true, PhotosRead),
	grant("GET", "/api/v1/photos/assets/*/original", true, PhotosRead),
	grant("HEAD", "/api/v1/photos/assets/*/original", true, PhotosRead),
	grant("POST", "/api/v1/photos/folders", true, PhotosWrite),
	grant("GET", "/api/v1/photos/trash", true, PhotosRead),
	grant("POST", "/api/v1/photos/trash/restore", true, PhotosWrite),
	grant("POST", "/api/v1/photos/selections", true, PhotosRead),
	// This endpoint also exports archives; require read as well as mutation rights.
	grant("POST", "/api/v1/photos/selection-actions", true, PhotosRead, PhotosWrite),
	grant("GET", "/api/v1/photos/archives", true, PhotosRead),
	grant("DELETE", "/api/v1/photos/archives", true, PhotosWrite),
	grant("POST", "/api/v1/photos/grabs", true, PhotosRead, GrabsWrite),
	grant("GET", "/api/v1/photos/duplicates", true, LegacyPhotos),
	grant("POST", "/api/v1/photos/duplicates", true, LegacyPhotos),
	grant("GET", "/api/v1/photos/metadata-jobs", true, LegacyPhotos),
	grant("POST", "/api/v1/photos/metadata-jobs", true, LegacyPhotos),
	grant("GET", "/api/v1/photos/collections", false, PhotosRead),
	grant("POST", "/api/v1/photos/collections", false, PhotosWrite),
	grant("PATCH", "/api/v1/photos/collections/*", false, PhotosWrite),
	grant("DELETE", "/api/v1/photos/collections/*", false, PhotosWrite),
	grant("POST", "/api/v1/photos/source-collections", false, PhotosWrite),
	grant("POST", "/api/v1/photos/source-memberships", false, PhotosWrite),
	grant("POST", "/api/v1/photos/uploads", true, PhotosWrite),
	grant("GET", "/api/v1/photos/uploads/*", true, PhotosWrite),
	grant("GET", "/api/v1/photos/uploads/*/parts", false, PhotosWrite),
	grant("POST", "/api/v1/photos/uploads/*/retry", false, PhotosWrite),
	grant("DELETE", "/api/v1/photos/uploads/*", true, PhotosWrite),
	grant("PATCH", "/api/v1/photos/uploads/*", true, PhotosWrite),
	grant("POST", "/api/v1/photos/uploads/*/finalize", true, PhotosWrite),
	grant("PATCH", "/api/v1/photos/uploads/*/components/*", true, PhotosWrite),
	grant("PUT", "/api/v1/photos/uploads/*/components/*/parts/*", false, PhotosWrite),
	grant("GET", "/api/v1/files/*", false, FilesRead),
	grant("GET", "/api/v1/files/*/content", false, FilesRead),
	grant("HEAD", "/api/v1/files/*/content", false, FilesRead),
	grant("POST", "/api/v1/files/sync/checkpoint", false, FilesRead),
	grant("GET", "/api/v1/backups/sources", false, BackupWrite, FilesWrite),
	grant("POST", "/api/v1/backups/sources", false, BackupWrite, FilesWrite),
	grant("PATCH", "/api/v1/backups/sources/*", false, BackupWrite, FilesWrite),
	grant("POST", "/api/v1/backups/uploads", false, BackupWrite, FilesWrite),
	grant("GET", "/api/v1/backups/uploads/*", false, BackupWrite, FilesWrite),
	grant("GET", "/api/v1/backups/uploads/*/parts", false, BackupWrite, FilesWrite),
	grant("POST", "/api/v1/backups/uploads/*/retry", false, BackupWrite, FilesWrite),
	grant("PUT", "/api/v1/backups/uploads/*/components/*/parts/*", false, BackupWrite, FilesWrite),
	grant("DELETE", "/api/v1/backups/uploads/*", false, BackupWrite, FilesWrite),
	grant("POST", "/api/v1/backups/uploads/*/finalize", false, BackupWrite, FilesWrite),
	grant("PATCH", "/api/v1/backups/uploads/*", false, BackupWrite, FilesWrite),
	grant("PUT", "/api/v1/backups/uploads/*/parts/*", false, BackupWrite, FilesWrite),
	grant("GET", "/api/library", false, FilesRead),
	grant("GET", "/api/library/page", false, FilesRead),
	grant("GET", "/api/library/search/page", false, FilesRead),
	grant("GET", "/api/library/thumbnail", false, FilesRead),
	grant("GET", "/api/library/music", false, FilesRead),
	grant("GET", "/api/library/capability", false, FilesRead),
	grant("GET", "/api/library/events", false, FilesRead),
	grant("PUT", "/api/library", false, FilesWrite),
	grant("DELETE", "/api/library", false, FilesWrite),
	grant("POST", "/api/library/folder", false, FilesWrite),
	grant("POST", "/api/library/rename", false, FilesWrite),
	grant("POST", "/api/library/copy", false, FilesRead, FilesWrite),
	grant("GET", "/api/trash", false, FilesRead),
	grant("DELETE", "/api/trash", false, FilesWrite),
	grant("POST", "/api/trash/restore", false, FilesWrite),
	grant("GET", "/api/library/archive", false, FilesRead),
	grant("POST", "/api/library/archive", false, FilesRead),
	grant("DELETE", "/api/library/archive", false, FilesRead),
	grant("GET", "/api/capsules", false, GrabsRead),
	grant("GET", "/api/v1/grabs/operations", false, GrabsRead),
	grant("POST", "/api/capsules", false, GrabsWrite, FilesRead),
	grant("DELETE", "/api/capsules", false, GrabsWrite),
	grant("GET", "/api/quota", false, StorageRead),
}

func routeMatches(pattern, path string) bool {
	a, b := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == "*" {
			if b[i] == "" || b[i] == "." || b[i] == ".." || strings.ContainsAny(b[i], "\\\x00") {
				return false
			}
		} else if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AllowsRequest does not authorize a resource: handlers must still verify owner,
// device and operation kind. Generic /api/uploads is deliberately excluded until
// its sessions carry those bindings; files:write cannot reach coordinator staging.
func (d Device) AllowsRequest(r *http.Request) bool {
	path := r.URL.Path
	if r.Method == "GET" && (path == "/api/v1/devices" || path == "/api/v1/devices/"+d.ID || path == "/api/v1/mobile/capabilities" || path == "/api/v1/mobile/profile" || path == "/api/v1/mobile/status") {
		return true
	}
	if r.Method == "POST" && (path == "/api/v1/devices/revoke" || path == "/api/v1/devices/"+d.ID+"/rotate" || path == "/api/v1/devices/"+d.ID+"/revoke") {
		return true
	}
	for _, g := range deviceGrants {
		if g.method == r.Method && routeMatches(g.path, path) {
			return d.Scopes == nil && g.legacy || d.HasScopes(g.scopes...)
		}
	}
	return false
}
