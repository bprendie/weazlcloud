package share

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bprendie/weazlcloud/internal/capsule"
)

func (h *Handler) gallery(w http.ResponseWriter, r *http.Request, id, rest string) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Passphrase string   `json:"passphrase"`
		Session    string   `json:"session"`
		IDs        []string `json:"ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if r.ParseForm() != nil {
			writeGone(w, capsule.ErrPhrase)
			return
		}
		body.Passphrase, body.Session = r.PostForm.Get("passphrase"), r.PostForm.Get("session")
		body.IDs = r.PostForm["ids"]
	} else if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "invalid gallery request", 400)
		return
	}
	auth := capsule.GalleryAuth{Passphrase: body.Passphrase, Session: body.Session}
	if rest == "gallery" {
		if !h.limit.Allow(id + ":" + remoteHost(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many attempts", 429)
			return
		}
		manifest, session, err := h.store.RenewGallerySession(id, auth)
		if err != nil {
			writeGone(w, err)
			return
		}
		h.limit.Reset(id + ":" + remoteHost(r))
		writeJSON(w, 200, map[string]any{"gallery": manifest, "session": session})
		return
	}
	if body.Session == "" {
		writeGone(w, capsule.ErrPhrase)
		return
	}
	parts := strings.Split(rest, "/")
	if rest == "zip/jobs" {
		job, err := h.store.PrepareGalleryZIP(id, auth, body.IDs)
		if err != nil {
			writeGone(w, err)
			return
		}
		writeJSON(w, 202, job)
		return
	}
	if len(parts) == 3 && parts[0] == "zip" && parts[1] == "jobs" {
		job, err := h.store.GalleryZIPStatus(id, auth, parts[2])
		if err != nil {
			writeGone(w, err)
			return
		}
		writeJSON(w, 200, job)
		return
	}
	if len(parts) == 2 && parts[0] == "preview" {
		var raw bytes.Buffer
		item, err := h.store.GalleryPreview(id, auth, parts[1], &raw)
		defer clear(raw.Bytes())
		if err != nil {
			writeGone(w, err)
			return
		}
		w.Header().Set("Content-Type", item.PreviewType)
		w.Header().Set("Content-Length", strconv.Itoa(raw.Len()))
		_, _ = w.Write(raw.Bytes())
		return
	}
	// A transfer is an explicit POST. Bots, HEAD and Range probes cannot spend
	// downloads; reconnecting requires a new explicit transfer and retry.
	if r.Header.Get("Range") != "" {
		http.Error(w, "range transfers are not available for gallery grabs", 416)
		return
	}
	started := false
	if len(parts) == 4 && parts[0] == "zip" && parts[1] == "jobs" && parts[3] == "download" {
		_, err := h.store.DownloadGalleryZIP(id, auth, parts[2], func(rec capsule.Record) (io.Writer, error) {
			galleryDownloadHeaders(w, rec, rec.Name)
			started = true
			return w, nil
		})
		if err != nil && !started {
			writeGone(w, err)
		}
		return
	}
	if len(parts) == 2 && parts[0] == "original" {
		_, err := h.store.GalleryOriginal(id, auth, parts[1], func(rec capsule.Record, item capsule.GalleryItem) (io.Writer, error) {
			galleryDownloadHeaders(w, rec, item.Name)
			w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
			started = true
			return w, nil
		})
		if err != nil && !started {
			writeGone(w, err)
		}
		return
	}
	if rest == "zip" {
		_, err := h.store.GalleryDownload(id, auth, body.IDs, func(rec capsule.Record) (io.Writer, error) {
			galleryDownloadHeaders(w, rec, rec.Name)
			started = true
			return w, nil
		})
		if err != nil && !started {
			writeGone(w, err)
		}
		return
	}
	http.NotFound(w, r)
}

func galleryDownloadHeaders(w http.ResponseWriter, rec capsule.Record, name string) {
	w.Header().Set("X-Weazl-Grabs-Remaining", strconv.Itoa(max(0, rec.Limit-rec.Used)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFilename(name)+`"`)
}
