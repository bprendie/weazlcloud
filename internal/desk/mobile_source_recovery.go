package desk

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type sourceRecoveryCursor struct {
	Kind  string `json:"kind"`
	Query string `json:"query"`
	After string `json:"after"`
}

// Called through multiGuard. photos:read authorizes an owner-wide metadata read;
// no supplied owner/device ID may change the authenticated owner's catalog.
func (h *Handler) mobileSourceRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]string{"code": "method_not_allowed"})
		return
	}
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var in struct {
		IDs       []string `json:"source_ids"`
		Namespace string   `json:"namespace"`
		Limit     int      `json:"limit"`
		Cursor    string   `json:"cursor"`
	}
	in.Limit = catalog.SourceRecoveryMaximum
	bad := func() {
		writeJSON(w, 400, map[string]string{"code": "invalid_request", "error": "invalid source collection lookup or cursor"})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || catalog.ValidateSourceRecovery(in.IDs, in.Namespace, in.Limit) != nil || len(in.Cursor) > 2048 {
		bad()
		return
	}
	sort.Strings(in.IDs)
	ids, _ := json.Marshal(in.IDs)
	query := catalog.SafeSourceKey(in.Namespace, string(ids))
	clear(ids)
	cursor := sourceRecoveryCursor{Kind: "source-collection-recovery-v1", Query: query}
	if in.Cursor != "" {
		wrapped, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil {
			bad()
			return
		}
		plain, e := res.Vault.Unwrap(wrapped)
		if e != nil {
			if !res.Vault.Unlocked() {
				mobilePartError(w, e)
			} else {
				bad()
			}
			return
		}
		defer clear(plain)
		cursor = sourceRecoveryCursor{}
		if json.Unmarshal(plain, &cursor) != nil || cursor.Kind != "source-collection-recovery-v1" || cursor.Query != query || len(cursor.After) != 43 {
			bad()
			return
		}
	}
	page, err := res.Lib.LookupPhotoSourceCollections(r.Context(), in.IDs, in.Namespace, cursor.After, in.Limit)
	if err != nil {
		mobilePartError(w, err)
		return
	}
	if page.HasMore {
		cursor.After = page.After
		plain, _ := json.Marshal(cursor)
		wrapped, e := res.Vault.Wrap(plain)
		clear(plain)
		if e != nil {
			mobilePartError(w, e)
			return
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(wrapped)
	}
	writeJSON(w, 200, page)
}
