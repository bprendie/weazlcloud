package desk

import (
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) listFolderPage(w http.ResponseWriter, r *http.Request) {
	h.listFolderPageFor(w, r, h.vault, h.lib)
}

func (h *Handler) searchFolderPage(w http.ResponseWriter, r *http.Request) {
	h.searchFolderPageFor(w, r, h.vault, h.lib)
}

func (h *Handler) listFolderPageFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := l.ListFolderPage(r.Context(), r.URL.Query().Get("path"), r.URL.Query().Get("sort"), r.URL.Query().Get("desc") == "1", limit, r.URL.Query().Get("cursor"))
	if err != nil {
		apiError(w, err)
		return
	}
	if !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	writeFolderPage(w, page)
}

func (h *Handler) searchFolderPageFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := l.SearchPage(r.Context(), library.SearchOptions{
		Query: r.URL.Query().Get("q"), Scope: r.URL.Query().Get("scope"),
		Type: r.URL.Query().Get("type"), Date: r.URL.Query().Get("date"), Size: r.URL.Query().Get("size"),
		Sort: r.URL.Query().Get("sort"), Descending: r.URL.Query().Get("desc") == "1",
		Limit: limit, Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		apiError(w, err)
		return
	}
	if !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	writeFolderPage(w, page)
}

func writeFolderPage(w http.ResponseWriter, page library.FolderPage) {
	files := make([]fileView, 0, len(page.Files))
	for _, f := range page.Files {
		files = append(files, fileView{Path: f.Path, Folder: f.Folder, Size: f.Size, Mtime: f.Mtime})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "next_cursor": page.NextCursor, "generation": page.Generation})
}

func (h *Handler) multiSearchFolderPage(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	h.searchFolderPageFor(w, r, res.Vault, res.Lib)
}
