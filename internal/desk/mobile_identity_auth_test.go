package desk

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileGuardAuthenticationAndScopeErrors(t *testing.T) {
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	device, token, err := store.CreateScopedDevice(owner.ID, "reader", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := store.Login(owner)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(store, capsule.New(filepath.Join(dir, "caps")), quota.New(dir), "", "", dir)
	check := func(token string, want int, code string) {
		t.Helper()
		for _, path := range []string{
			"/api/v1/photos/uploads",
			"/api/v1/photos/uploads/0123456789abcdef0123456789abcdef/retry",
			"/api/v1/photos/source-collections",
		} {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"transport":"parts-v1"}`))
			r.Header.Set("X-Weazl-Desk", "1")
			r.Header.Set("Authorization", "Bearer "+token)
			// Invalid bearer credentials must never fall back to a valid owner cookie.
			r.AddCookie(&http.Cookie{Name: "weazl_session", Value: cookie})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("%s: status=%d want=%d body=%s", path, w.Code, want, w.Body.String())
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatal("authentication error must be JSON", w.Header())
			}
			var body map[string]string
			decoder := json.NewDecoder(w.Body)
			if err := decoder.Decode(&body); err != nil || body["code"] != code || body["error"] == "" {
				t.Fatalf("%s: error body=%v decode=%v", path, body, err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("%s: router appended a second response: %v", path, err)
			}
		}
	}
	check(token, http.StatusForbidden, "insufficient_scope")
	check(strings.Repeat("f", 64), http.StatusUnauthorized, "authentication_required")
	now = device.ExpiresAt
	check(token, http.StatusUnauthorized, "authentication_required")
	now = now.Add(-time.Minute)
	if err := store.RevokeDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	check(token, http.StatusUnauthorized, "authentication_required")
}
