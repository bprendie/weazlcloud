package users

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLegacyRecordWithoutScopeOrGenerationRetainsIdentity(t *testing.T) {
	s, u, now := deviceFixture(t)
	d, token, err := s.CreateDevice(u.ID, "legacy phone")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f["state_version"] = 1
	record := f["devices"].([]any)[0].(map[string]any)
	delete(record, "scopes")
	delete(record, "generation")
	delete(record, "authorization_version")
	delete(record, "revoked")
	legacyHash := record["token_hash"]
	raw, err = json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(s.path, s.userRoot)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetClock(func() time.Time { return *now })
	got, ok := reloaded.ActiveDevice(u.ID, d.ID)
	if !ok || got.ID != d.ID || got.Generation != 1 || !got.ExpiresAt.Equal(d.ExpiresAt) || !HasScope(got, PhotosWrite) || HasScope(got, FilesWrite) {
		t.Fatal("legacy identity/grants changed")
	}
	if _, err := reloaded.Current(deviceRequest("GET", "/api/v1/photos", token)); err != nil {
		t.Fatal(err)
	}
	// A later save persists additive state without changing the old token hash.
	if _, err := reloaded.UpdateProfile(u.ID, "Owner"); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f["devices"].([]any)[0].(map[string]any)["token_hash"] != legacyHash {
		t.Fatal("legacy token hash changed")
	}
}

func TestExpiredDeviceOwnerReauthorizesSameIdentity(t *testing.T) {
	s, u, now := deviceFixture(t)
	d, token, err := s.CreateScopedDevice(u.ID, "phone", []string{FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := s.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(deviceTTL)
	if _, ok := s.ActiveDevice(u.ID, d.ID); ok {
		t.Fatal("expired active identity")
	}
	r := deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", token)
	if _, err := s.RotateDevice(r, d.ID, "renew", 1, strings.Repeat("a", 64)); err == nil {
		t.Fatal("expired credential renewed itself")
	}
	r = httptest.NewRequest("POST", "/api/v1/devices/"+d.ID+"/reauthorize", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	got, err := s.ReauthorizeDevice(r, d.ID, strings.Repeat("a", 64), nil)
	if err != nil || got.ID != d.ID || !HasScope(got, FilesRead) || got.Generation != 2 {
		t.Fatal("expired identity replaced", err)
	}
}
