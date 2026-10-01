package desk

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) multiPhotoGrab(w http.ResponseWriter, r *http.Request) {
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !strings.HasPrefix(h.grabBase(), "https://") {
		apiError(w, capsule.ErrNeedBase)
		return
	}
	var body struct {
		Selection     string   `json:"selection_id"`
		IDs           []string `json:"ids"`
		Hidden        bool     `json:"hidden"`
		ConfirmHidden bool     `json:"confirm_hidden"`
		Title         string   `json:"title"`
		Gate          string   `json:"gate"`
		Passphrase    string   `json:"passphrase"`
		Expiry        string   `json:"expiry"`
		Grabs         int      `json:"grabs"`
	}
	if !decodeBody(w, r, &body, 2<<20) {
		return
	}
	if body.Hidden && !body.ConfirmHidden || len(body.Title) > 200 || body.Grabs < 1 || body.Grabs > 10000 || body.Gate != "open" && body.Gate != "passphrase" {
		writeJSON(w, 400, map[string]string{"error": "invalid gallery options or missing explicit hidden sharing confirmation"})
		return
	}
	var exports []library.PhotoExport
	var release func()
	if body.Selection != "" {
		if len(body.IDs) != 0 {
			writeJSON(w, 400, map[string]string{"error": "choose a selection or asset IDs"})
			return
		}
		exports, release, err = res.Lib.PreparePhotoSelectionExport(r.Context(), body.Selection, body.Hidden)
	} else {
		exports, release, err = res.Lib.PreparePhotoExport(r.Context(), body.IDs, body.Hidden)
	}
	if err != nil {
		photoAPIError(w, err)
		return
	}
	defer release()
	var bytes int64
	sources := make([]capsule.GallerySource, 0, len(exports))
	for _, item := range exports {
		// Frozen originals, ZIP, authenticated framing and the bounded preview.
		if item.Size > ((1<<63-1)-bytes-(9<<20))/2 {
			writeJSON(w, 400, map[string]string{"error": "gallery size overflow"})
			return
		}
		bytes += 2*item.Size + (8 << 20) + (1 << 20)
		preview := func() ([]byte, string, error) {
			body, kind, err := item.Preview()
			if err != nil && !errors.Is(err, vault.ErrLocked) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
				// The original was already frozen. A bad/unsupported derivative
				// is a placeholder, not failure of an otherwise valid album grab.
				clear(body)
				return nil, "", nil
			}
			return body, kind, err
		}
		sources = append(sources, capsule.GallerySource{Item: capsule.GalleryItem{Name: item.Name, MediaType: item.MediaType, Size: item.Size, Revision: item.Revision}, Original: capsule.StreamSource(item.Original), Preview: preview})
	}
	if h.quota != nil {
		used, err := res.Lib.Usage(r.Context())
		if err != nil {
			photoAPIError(w, err)
			return
		}
		done, err := h.quota.Reserve(user.ID, h.users.Count(), used, 0, bytes)
		if err != nil {
			photoAPIError(w, err)
			return
		}
		defer done()
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "Photos"
	}
	rec, err := h.caps.MintGallery(capsule.Record{Owner: user.ID, Label: title, Name: title + ".zip", Gate: body.Gate, Expires: time.Now().Add(parseExpiry(body.Expiry)), Limit: body.Grabs}, body.Passphrase, sources)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	view := rec.View()
	view["url"] = strings.TrimRight(h.grabBase(), "/") + "/g/" + rec.ID
	writeJSON(w, http.StatusCreated, view)
}
