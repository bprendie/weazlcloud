package desk

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
)

func mobileFilesETag(owner string, item library.MobileFileItem) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + item.ID + "\x00" + strconv.FormatUint(item.Revision, 10)))
	return `"files-` + hex.EncodeToString(sum[:]) + `"`
}

func mobileFilesTagMatch(raw, etag string, weak bool) bool {
	for _, tag := range strings.Split(raw, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || tag == etag || weak && strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}

func mobileFilesPrecondition(r *http.Request, etag string, modified time.Time) int {
	if value := r.Header.Get("If-Match"); value != "" {
		if !mobileFilesTagMatch(value, etag, false) {
			return 412
		}
	} else if date, err := http.ParseTime(r.Header.Get("If-Unmodified-Since")); err == nil && modified.Truncate(time.Second).After(date) {
		return 412
	}
	if value := r.Header.Get("If-None-Match"); value != "" {
		if mobileFilesTagMatch(value, etag, true) {
			return 304
		}
	} else if date, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !modified.Truncate(time.Second).After(date) {
		return 304
	}
	return 0
}

func mobileFilesIfRange(raw, etag string, modified time.Time) bool {
	if raw == "" || raw == etag {
		return true
	}
	// HTTP dates have second precision; require exact Last-Modified agreement.
	date, err := http.ParseTime(raw)
	return err == nil && modified.Truncate(time.Second).Equal(date)
}

func (h *Handler) mobileFileContent(w http.ResponseWriter, r *http.Request, id string) {
	res, user, err := h.currentResource(r)
	if err != nil {
		mobileFilesError(w, err)
		return
	}
	started := false
	err = res.Lib.ReadMobileFile(r.Context(), id, func(item library.MobileFileItem, source library.PhotoRangeSource) error {
		authorize := func() error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			current, err := h.users.Current(r)
			if err != nil {
				return err
			}
			if current.ID != user.ID {
				return users.ErrNoSession
			}
			return nil
		}
		if err := authorize(); err != nil {
			return err
		}
		etag := mobileFilesETag(user.ID, item)
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Last-Modified", item.Mtime.UTC().Format(http.TimeFormat))
		media := mime.TypeByExtension(path.Ext(item.Path))
		if media == "" {
			media = "application/octet-stream"
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(item.Path)}))
		if status := mobileFilesPrecondition(r, etag, item.Mtime); status != 0 {
			started = true
			if status == http.StatusPreconditionFailed {
				nativeJSONcode(w, status, "precondition_failed", "file revision precondition failed")
			} else {
				w.WriteHeader(status)
			}
			return nil
		}
		status, offset, length := http.StatusOK, int64(0), item.Size
		// Range is defined for GET; HEAD reports the full representation.
		if raw := r.Header.Get("Range"); r.Method == http.MethodGet && raw != "" && mobileFilesIfRange(r.Header.Get("If-Range"), etag, item.Mtime) {
			first, last, ok := byteRange(raw, item.Size)
			if !ok {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(item.Size, 10))
				started = true
				nativeJSONcode(w, 416, "invalid_range", "invalid single byte range")
				return nil
			}
			offset, length, status = first, last-first+1, http.StatusPartialContent
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(first, 10)+"-"+strconv.FormatInt(last, 10)+"/"+strconv.FormatInt(item.Size, 10))
		}
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		started = true
		w.WriteHeader(status)
		if r.Method == http.MethodHead || length == 0 {
			return nil
		}
		return source(offset, length, mobileFilesAuthorizedWriter{w, authorize})
	})
	if err != nil && !started {
		mobileFilesError(w, err)
	}
}

// Revalidate before each output chunk so revoke/expiry stops an active read.
type mobileFilesAuthorizedWriter struct {
	output    io.Writer
	authorize func() error
}

func (w mobileFilesAuthorizedWriter) Write(p []byte) (int, error) {
	if err := w.authorize(); err != nil {
		return 0, err
	}
	return w.output.Write(p)
}
