package desk

import (
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) multiPhotoOriginal(w http.ResponseWriter, r *http.Request, id string) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	started := false
	err = res.Lib.ReadPhotoOriginal(r.Context(), id, r.URL.Query().Get("hidden") == "1", func(item library.PhotoItem, source library.PhotoRangeSource) error {
		etag := `"` + item.ID + "-" + strconv.FormatUint(item.Revision, 10) + `"`
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", item.MediaType)
		disposition := "inline"
		if r.URL.Query().Get("download") == "1" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(item.Path)}))
		if r.Header.Get("If-None-Match") == etag {
			started = true
			w.WriteHeader(304)
			return nil
		}
		status := http.StatusOK
		start, length := int64(0), item.Size
		if raw := r.Header.Get("Range"); raw != "" && (r.Header.Get("If-Range") == "" || r.Header.Get("If-Range") == etag) {
			first, last, ok := byteRange(raw, item.Size)
			if !ok {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(item.Size, 10))
				started = true
				w.WriteHeader(416)
				return nil
			}
			start, length, status = first, last-first+1, http.StatusPartialContent
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(first, 10)+"-"+strconv.FormatInt(last, 10)+"/"+strconv.FormatInt(item.Size, 10))
		}
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		started = true
		w.WriteHeader(status)
		if r.Method == http.MethodHead {
			return nil
		}
		return source(start, length, w)
	})
	if err != nil && !started {
		if errors.Is(err, catalog.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			apiError(w, err)
		}
	}
}
