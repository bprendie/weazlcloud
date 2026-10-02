package users

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReauthorizationPreservesIDAndInvalidatesJobs(t *testing.T) {
	s, u, _ := deviceFixture(t)
	d, old, _ := s.CreateScopedDevice(u.ID, "phone", []string{PhotosWrite})
	grant, err := s.GrantForRequest(deviceRequest("POST", "/api/v1/photos/uploads", old), PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(u.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	replacement := strings.Repeat("b", 64)
	bearer := deviceRequest("POST", "/api/v1/devices/"+d.ID+"/reauthorize", old)
	if _, err := s.ReauthorizeDevice(bearer, d.ID, replacement, nil); err == nil {
		t.Fatal("bearer resurrected itself")
	}
	foreign, err := s.Create("another", "test-password", false)
	if err != nil {
		t.Fatal(err)
	}
	foreignSession, _ := s.Login(foreign)
	r := httptest.NewRequest("POST", bearer.URL.Path, nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: foreignSession})
	if _, err := s.ReauthorizeDevice(r, d.ID, replacement, nil); err == nil {
		t.Fatal("foreign reauthorization")
	}
	cookie, _ := s.Login(u)
	r = httptest.NewRequest("POST", bearer.URL.Path, nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	got, err := s.ReauthorizeDevice(r, d.ID, replacement, nil)
	if err != nil || got.ID != d.ID || !HasScope(got, PhotosWrite) {
		t.Fatalf("reauthorize: %#v %v", got, err)
	}
	if _, ok := s.ActiveDevice(u.ID, d.ID); !ok {
		t.Fatal("reauthorized inactive")
	}
	if err := s.CheckDeviceGrant(grant, PhotosWrite); err == nil {
		t.Fatal("old authorization job revived")
	}
	if _, err := s.DeviceForRequest(deviceRequest("POST", "/api/v1/photos/uploads", old)); err == nil {
		t.Fatal("old generation revived")
	}
	// Reauthorization may intentionally drop all resource grants.
	got, err = s.ReauthorizeDevice(r, d.ID, strings.Repeat("c", 64), []string{})
	if err != nil || got.Scopes == nil || HasScope(got, PhotosWrite) {
		t.Fatal("empty grants expanded", err)
	}
}
func TestDisableKeepsIdentityButInvalidatesAllGenerations(t *testing.T) {
	s, _, _ := deviceFixture(t)
	u, err := s.Create("member", "test-password", false)
	if err != nil {
		t.Fatal(err)
	}
	d, old, _ := s.CreateDevice(u.ID, "phone")
	replacement := strings.Repeat("a", 64)
	if _, err := s.RotateDevice(deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", old), d.ID, "rotate", 1, replacement); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ActiveDevice(u.ID, d.ID); ok {
		t.Fatal("disabled active")
	}
	if err := s.SetDisableError(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(u.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{old, replacement} {
		if _, err := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", token)); err == nil {
			t.Fatal("enable revived credentials")
		}
	}
	if len(s.Devices(u.ID)) != 1 {
		t.Fatal("disable erased identity")
	}
}
func TestTombstonesDontConsumeSlotsAndInstanceSurvivesRestart(t *testing.T) {
	s, u, now := deviceFixture(t)
	for range 34 {
		d, _, err := s.CreateDevice(u.ID, "phone")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeDevice(u.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.InstanceID()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(s.path, s.userRoot)
	if err != nil {
		t.Fatal(err)
	}
	next, err := reloaded.InstanceID()
	if err != nil || next != id {
		t.Fatal("instance changed", err)
	}
	reloaded.SetClock(func() time.Time { return *now })
	for range 32 {
		if _, _, err := reloaded.CreateDevice(u.ID, "active"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := reloaded.CreateDevice(u.ID, "over limit"); err != ErrDeviceLimit {
		t.Fatal("limit", err)
	}
	*now = now.Add(deviceTTL)
	if _, _, err := reloaded.CreateDevice(u.ID, "after expiry"); err != nil {
		t.Fatal("expired slots retained", err)
	}
}
