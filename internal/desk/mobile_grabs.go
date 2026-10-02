package desk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// tryMobileGrabs must run inside the existing authenticated owner guard.
// The parent router owns scope/CSRF/lifecycle admission. Unkeyed browser calls
// fall through unchanged. GET /api/v1/grabs/operations uses Idempotency-Key.
func (h *Handler) tryMobileGrabs(w http.ResponseWriter, r *http.Request) bool {
	key := r.Header.Get("Idempotency-Key")
	mint := r.Method == http.MethodPost && (r.URL.Path == "/api/capsules" || r.URL.Path == "/api/v1/photos/grabs")
	lookup := r.Method == http.MethodGet && r.URL.Path == "/api/v1/grabs/operations"
	if !lookup && (!mint || key == "") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if len(key) < 1 || len(key) > 200 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\x00") {
		writeJSON(w, 400, map[string]string{"error": "invalid Idempotency-Key"})
		return true
	}
	if h.caps == nil {
		writeJSON(w, 501, map[string]string{"error": "grabs unavailable"})
		return true
	}
	owner := ""
	v, l := h.vault, h.lib
	if h.users != nil {
		res, u, err := h.currentResource(r)
		if err != nil {
			apiUsersError(w, err)
			return true
		}
		owner, v, l = u.ID, res.Vault, res.Lib
	}
	if v == nil || !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return true
	}
	var result capsule.MobileResult
	var err error
	if lookup {
		result, err = h.caps.MobileLookup(v, owner, key)
	} else {
		base := h.publicBase
		if h.users != nil {
			base = h.grabBase()
		}
		if !strings.HasPrefix(base, "https://") {
			apiError(w, capsule.ErrNeedBase)
			return true
		}
		var spec []byte
		spec, err = canonicalMobileGrab(w, r)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad request"})
			return true
		}
		defer clear(spec)
		request, publish, release, authErr := h.mobileGrabAuthorization(r, v)
		if authErr != nil {
			apiUsersError(w, authErr)
			return true
		}
		defer release()
		r = request
		status := http.StatusOK
		if r.URL.Path == "/api/v1/photos/grabs" {
			status = http.StatusCreated
		}
		result, err = h.caps.MobileMint(v, owner, key, spec, base, status, mobileGrabPrepare(r, l), func(store *capsule.Store) capsule.MobileResult {
			var publicationErr error
			guardErr := store.GuardMobilePublication(func(commit func() error) error {
				publicationErr = publish(commit)
				return publicationErr
			})
			if guardErr != nil {
				return capsule.MobileResult{Status: 500, Body: []byte(`{"error":"grab storage failure"}`)}
			}
			// Construct only the dependencies these existing handlers use; Handler has
			// mutexes and must never be copied. The scoped Store shares the mint lock.
			delegate := &Handler{vault: v, lib: l, caps: store, publicBase: base, users: h.users, registry: h.registry, quota: h.quota}
			out := httptest.NewRecorder()
			if r.URL.Path == "/api/v1/photos/grabs" {
				if h.users == nil {
					writeJSON(out, 501, map[string]string{"error": "owner accounts required"})
				} else {
					delegate.multiPhotoGrab(out, r)
				}
			} else if h.users != nil {
				delegate.multiMintCapsule(out, r)
			} else {
				delegate.mintCapsule(out, r)
			}
			if publicationErr != nil || r.Context().Err() != nil {
				if h.users != nil && r.Header.Get("Authorization") != "" {
					if _, authErr := h.users.Current(r); authErr != nil {
						out = httptest.NewRecorder()
						apiUsersError(out, authErr)
					}
				}
			}
			return capsule.MobileResult{Status: out.Code, Body: bytes.Clone(out.Body.Bytes())}
		})
	}
	if errors.Is(err, capsule.ErrMobileConflict) {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return true
	}
	if err != nil {
		apiError(w, err)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
	return true
}

// JSON object key order and whitespace do not alter the owner-keyed spec hash.
// Preserve array order and explicit options; reject unknown/trailing input.
func canonicalMobileGrab(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	limit := int64(8192)
	if r.URL.Path == "/api/v1/photos/grabs" {
		limit = 2 << 20
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("expected JSON object")
	}
	var body any
	if r.URL.Path == "/api/capsules" {
		body = &mintBody{}
	} else {
		body = &mobileGalleryBody{}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(body); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(normalized))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(normalized)), nil }
	return json.Marshal(struct {
		Route string
		Body  json.RawMessage
	}{r.URL.Path, normalized})
}

func decodeMobileMint(r io.Reader, b *mintBody) error { return json.NewDecoder(r).Decode(b) }

type mobileGalleryBody struct {
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
