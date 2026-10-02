package users

import (
	"errors"
	"net/http"
	"testing"
)

func TestExplicitMethodGrantsLegacyAndEmptyPersistence(t *testing.T) {
	s, u, _ := deviceFixture(t)
	legacy, legacyToken, _ := s.CreateDevice(u.ID, "legacy")
	scoped, token, err := s.CreateScopedDevice(u.ID, "reader", []string{PhotosRead, FilesRead})
	if err != nil {
		t.Fatal(err)
	}
	empty, emptyToken, err := s.CreateScopedDevice(u.ID, "empty", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if !HasScope(legacy, PhotosWrite) || HasScope(legacy, FilesRead) || HasScope(empty, PhotosRead) {
		t.Fatal("legacy/empty scope mapping")
	}
	cases := []struct {
		method, path   string
		legacy, reader bool
	}{
		{"GET", "/api/v1/photos", true, true},
		{"POST", "/api/v1/photos/albums", true, false},
		{"POST", "/api/v1/photos/metadata-jobs", true, false},
		{"GET", "/api/v1/photos/collections", false, true},
		{"POST", "/api/v1/photos/source-collections", false, false},
		{"GET", "/api/v1/files/sync", false, true},
		{"HEAD", "/api/v1/files/file/content", false, true},
		{"DELETE", "/api/v1/files/file/content", false, false},
		{"GET", "/api/v1/photos/unknown", false, false},
		{"GET", "/api/admin/users", false, false},
		{"POST", "/api/unlock", false, false},
		{"POST", "/api/settings", false, false},
		{"POST", "/api/v1/devices", false, false},
		{"GET", "/api/uploads/session", false, false},
		{"POST", "/api/v1/devices/foreign/rotate", false, false},
		{"GET", "/api/v1/devices/foreign", false, false},
	}
	for _, c := range cases {
		for _, v := range []struct {
			token string
			want  bool
		}{{legacyToken, c.legacy}, {token, c.reader}} {
			_, err := s.DeviceForRequest(deviceRequest(c.method, c.path, v.token))
			if (err == nil) != v.want {
				t.Errorf("%s %s allowed=%v want=%v", c.method, c.path, err == nil, v.want)
			}
		}
	}
	reloaded, err := New(s.path, s.userRoot)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetClock(s.clock)
	devices := reloaded.Devices(u.ID)
	if len(devices) != 3 || devices[1].ID != scoped.ID || devices[2].Scopes == nil {
		t.Fatal("scope persistence")
	}
	if _, err := reloaded.DeviceForRequest(deviceRequest("GET", "/api/v1/photos", emptyToken)); !errors.Is(err, ErrInsufficientScope) {
		t.Fatal(err)
	}
	cookie, _ := s.Login(u)
	r := deviceRequest("GET", "/api/v1/photos", "bad")
	r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	if _, err := s.Current(r); err == nil {
		t.Fatal("invalid bearer fell back to cookie")
	}
}
func TestBackupGrantsAndGenericUploadIsolation(t *testing.T) {
	s, u, _ := deviceFixture(t)
	if _, _, err := s.CreateScopedDevice(u.ID, "invalid", []string{BackupWrite}); err == nil {
		t.Fatal("backup without files:write")
	}
	d, token, err := s.CreateScopedDevice(u.ID, "writer", []string{BackupWrite, FilesWrite, PhotosWrite})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/photos/source-memberships", "/api/v1/photos/source-collections", "/api/v1/backups/sources", "/api/v1/backups/uploads"} {
		if _, err := s.DeviceForRequest(deviceRequest("POST", path, token)); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{"/api/v1/photos/uploads/x/components/original/parts/0", "/api/v1/backups/uploads/x/parts/0"} {
		if _, err := s.DeviceForRequest(deviceRequest("PUT", path, token)); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
		if _, err := s.DeviceForRequest(deviceRequest(method, "/api/uploads/x", token)); err == nil {
			t.Fatal("generic upload bypass")
		}
	}
	r := deviceRequest("POST", "/api/v1/backups/uploads", token)
	if _, err := s.CheckDeviceOperation(r, u.ID, d.ID, "backup"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckDeviceOperation(r, u.ID, "foreign", "backup"); err == nil {
		t.Fatal("foreign device")
	}
	if _, err := s.CheckDeviceOperation(r, "foreign", d.ID, "backup"); err == nil {
		t.Fatal("foreign owner")
	}
}
