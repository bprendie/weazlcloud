package users

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestGrantPublicationSerializesRevocationAndExpires(t *testing.T) {
	s, u, now := deviceFixture(t)
	d, token, err := s.CreateScopedDevice(u.ID, "phone", []string{PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.GrantForRequest(deviceRequest("POST", "/api/v1/photos/uploads", token), PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeviceGrant(g, FilesWrite); !errors.Is(err, ErrInsufficientScope) {
		t.Fatal("grant scope bypass", err)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	order := make(chan string, 2)
	published, revoked := make(chan error, 1), make(chan error, 1)
	go func() {
		published <- s.WithDeviceGrant(g, func() error {
			close(entered)
			<-finish
			order <- "publish"
			return nil
		}, PhotosWrite)
	}()
	<-entered
	ready := make(chan struct{})
	go func() { close(ready); err := s.RevokeDevice(u.ID, d.ID); order <- "revoke"; revoked <- err }()
	<-ready
	close(finish)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if <-order != "publish" || <-order != "revoke" {
		t.Fatal("revocation crossed publication")
	}
	called := false
	if err := s.WithDeviceGrant(g, func() error { called = true; return nil }, PhotosWrite); err == nil || called {
		t.Fatal("revoked publication invoked")
	}
	replacement := strings.Repeat("a", 64)
	// Expiry remains the admitted generation's deadline despite proactive rotation.
	d, token, err = s.CreateScopedDevice(u.ID, "second", []string{PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.GrantForRequest(deviceRequest("POST", "/api/v1/photos/uploads", token), PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(deviceTTL - DeviceGrace)
	if _, err := s.RotateDevice(deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", token), d.ID, "renew", 1, replacement); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(DeviceGrace)
	if _, ok := s.ActiveDevice(u.ID, d.ID); !ok {
		t.Fatal("new generation inactive")
	}
	if err := s.CheckDeviceGrant(g, PhotosWrite); err == nil {
		t.Fatal("rotation extended job grant expiry")
	}
}
func TestFailedPersistenceDoesNotCommitRotationOrRevocation(t *testing.T) {
	s, u, _ := deviceFixture(t)
	d, old, err := s.CreateDevice(u.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.path = t.TempDir()
	r := deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", old)
	replacement := strings.Repeat("a", 64)
	if _, err := s.RotateDevice(r, d.ID, "rotate", 1, replacement); err == nil {
		t.Fatal("rotation persisted to directory")
	}
	if err := s.RevokeDevice(u.ID, d.ID); err == nil {
		t.Fatal("revocation persisted to directory")
	}
	s.path = path
	got, ok := s.ActiveDevice(u.ID, d.ID)
	if !ok || got.Generation != 1 {
		t.Fatal("failed persist mutated generation")
	}
	if _, err := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", old)); err != nil {
		t.Fatal("old token lost", err)
	}
	if _, err := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", replacement)); err == nil {
		t.Fatal("uncommitted token active")
	}
}
func TestFutureUserStateRejectedWithoutRewrite(t *testing.T) {
	s, _, _ := deviceFixture(t)
	raw := []byte(`{"state_version":99,"users":[],"devices":[]}`)
	if err := os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, open := range []func(string, string) (*Store, error){New, NewReadOnly} {
		if _, err := open(s.path, s.userRoot); err == nil {
			t.Fatal("future state accepted")
		}
	}
	got, err := os.ReadFile(s.path)
	if err != nil || string(got) != string(raw) {
		t.Fatal("future state rewritten", err)
	}
}
