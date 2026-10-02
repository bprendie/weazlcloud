package users

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func deviceFixture(t *testing.T) (*Store, User, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	return s, u, &now
}
func deviceRequest(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}
func TestRotationRecoveryGraceRestartAndCompetingOperations(t *testing.T) {
	s, u, now := deviceFixture(t)
	d, old, err := s.CreateScopedDevice(u.ID, "phone", []string{PhotosWrite, FilesWrite, BackupWrite})
	if err != nil {
		t.Fatal(err)
	}
	replacement := strings.Repeat("a", 64)
	r := deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", old)
	grant, err := s.GrantForRequest(deviceRequest("POST", "/api/v1/photos/uploads", old), PhotosWrite)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.RotateDevice(r, d.ID, "rotate-1", 1, replacement)
	if err != nil || next.ID != d.ID || next.Generation != 2 {
		t.Fatalf("rotation: %#v %v", next, err)
	}
	for _, token := range []string{old, replacement} {
		retry := deviceRequest("POST", r.URL.Path, token)
		got, err := s.RotateDevice(retry, d.ID, "rotate-1", 1, replacement)
		if err != nil || got.Generation != 2 {
			t.Fatalf("lost response retry: %v", err)
		}
	}
	if _, err := s.RotateDevice(r, d.ID, "different", 2, strings.Repeat("b", 64)); !errors.Is(err, ErrRotationConflict) {
		t.Fatalf("previous token rotation: %v", err)
	}
	if _, err := s.RotateDevice(r, d.ID, "rotate-1", 1, strings.Repeat("b", 64)); !errors.Is(err, ErrRotationConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	reloaded, err := New(s.path, s.userRoot)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetClock(func() time.Time { return *now })
	if _, err := reloaded.RotateDevice(r, d.ID, "rotate-1", 1, replacement); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.CheckDeviceGrant(grant, PhotosWrite); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(DeviceGrace)
	if _, err := reloaded.DeviceForRequest(r); !errors.Is(err, ErrNoSession) {
		t.Fatalf("grace boundary: %v", err)
	}
	current := deviceRequest("GET", "/api/v1/mobile/capabilities", replacement)
	if _, err := reloaded.DeviceForRequest(current); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.path)
	if strings.Contains(string(raw), old) || strings.Contains(string(raw), replacement) {
		t.Fatal("raw token persisted")
	}
	if err := reloaded.RevokeDevice(u.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.ActiveDevice(u.ID, d.ID); ok {
		t.Fatal("revoked active device")
	}
	if err := reloaded.CheckDeviceGrant(grant, PhotosWrite); err == nil {
		t.Fatal("revoked grant")
	}
}
func TestConcurrentRotationOnlyOneWinner(t *testing.T) {
	s, u, _ := deviceFixture(t)
	d, token, _ := s.CreateDevice(u.ID, "phone")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, value := range []string{"a", "b"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			_, err := s.RotateDevice(deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", token), d.ID, value, 1, strings.Repeat(value, 64))
			results <- err
		}(value)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrRotationConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
func TestGenerationExpiryAndPasswordInvalidatesGrace(t *testing.T) {
	s, u, now := deviceFixture(t)
	d, old, _ := s.CreateDevice(u.ID, "phone")
	replacement := strings.Repeat("a", 64)
	*now = now.Add(deviceTTL - time.Minute)
	r := deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", old)
	if _, err := s.RotateDevice(r, d.ID, "renew", 1, replacement); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if _, err := s.DeviceForRequest(r); err == nil {
		t.Fatal("grace extended expired generation")
	}
	if _, ok := s.ActiveDevice(u.ID, d.ID); !ok {
		t.Fatal("current generation expired early")
	}
	latest := strings.Repeat("c", 64)
	if _, err := s.RotateDevice(deviceRequest("POST", "/api/v1/devices/"+d.ID+"/rotate", replacement), d.ID, "renew-again", 2, latest); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{replacement, latest} {
		if _, err := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", secret)); err != nil {
			t.Fatal("live generation before password change", err)
		}
	}
	if err := s.ChangePassword(u.ID, "test-password", "next-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{old, replacement, latest} {
		if _, err := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", token)); err == nil {
			t.Fatal("password left credential active")
		}
	}
	if len(s.Devices(u.ID)) != 1 || s.Devices(u.ID)[0].ID != d.ID {
		t.Fatal("password erased identity")
	}
}
