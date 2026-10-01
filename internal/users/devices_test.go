package users

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceCredentialsPersistScopedRevocationAndPasswordChange(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Create("owner", "test-password", true)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := s.CreateDevice(u.ID, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if device.TokenHash != "" || device.PasswordVersion != "" {
		t.Fatal("device view contains credential material")
	}
	raw, _ := os.ReadFile(s.path)
	if strings.Contains(string(raw), token) {
		t.Fatal("raw device token persisted")
	}
	reloaded, err := New(s.path, s.userRoot)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/photos", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if got, err := reloaded.Current(req); err != nil || got.ID != u.ID {
		t.Fatalf("device cannot authenticate after restart: %v", err)
	}
	req.URL.Path = "/api/admin/users"
	if _, err := reloaded.Current(req); err == nil {
		t.Fatal("Photos device credential authenticated admin API")
	}
	req.URL.Path = "/api/v1/photos"
	if err := reloaded.RevokeDevice("different-owner", device.ID); err == nil {
		t.Fatal("cross-owner revoke succeeded")
	}
	if err := reloaded.RevokeDevice(u.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Current(req); err == nil {
		t.Fatal("revoked credential authenticated")
	}
	_, newToken, err := reloaded.CreateDevice(u.ID, "Phone 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ChangePassword(u.ID, "test-password", "another-password"); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+newToken)
	if _, err := reloaded.Current(req); err == nil {
		t.Fatal("password change left old credential usable")
	}
}
