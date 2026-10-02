package desk

import (
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/users"
)

func mobileGrantFixture(t *testing.T, scopes []string) (*Handler, *users.Device, string) {
	t.Helper()
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := us.Create("grant", "grant-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(us, capsule.New(filepath.Join(dir, "capsules")), nil, "https://grab.test", "", dir)
	res := h.registry.For(owner)
	if err := res.Vault.Forge([]byte("test"), []byte("test")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.LockVault() })
	device, token, err := us.CreateScopedDevice(owner.ID, "phone", scopes)
	if err != nil {
		t.Fatal(err)
	}
	return h, &device, token
}

func TestMobileGrabGrantRejectsRevokeAtFinalPublication(t *testing.T) {
	for _, kind := range []string{"file", "gallery"} {
		t.Run(kind, func(t *testing.T) {
			route := "/api/capsules"
			readScope := users.FilesRead
			if kind == "gallery" {
				route = "/api/v1/photos/grabs"
				readScope = users.PhotosRead
			}
			h, device, token := mobileGrantFixture(t, []string{users.GrabsWrite, readScope})
			res := h.registry.For(h.users.Users()[0])
			req := httptest.NewRequest("POST", route, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req, publish, release, err := h.mobileGrabAuthorization(req, res.Vault)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			source := func(dst io.Writer) error {
				if _, err := io.WriteString(dst, "abc"); err != nil {
					return err
				}
				// This executes inside the capsule storage lock, before metadata exists.
				// Revoke must remain callable here: sealing cannot hold users.mu.
				return h.users.RevokeDevice(device.OwnerID, device.ID)
			}
			got, err := h.caps.MobileMint(res.Vault, device.OwnerID, "revoked", []byte("spec"), "https://grab.test", 200, nil, func(scoped *capsule.Store) capsule.MobileResult {
				if err := scoped.GuardMobilePublication(publish); err != nil {
					t.Fatal(err)
				}
				rec := capsule.Record{Owner: device.OwnerID, Name: "frozen", Gate: "open", Limit: 2, Expires: time.Now().Add(time.Hour)}
				var err error
				if kind == "gallery" {
					_, err = scoped.MintGallery(rec, "", []capsule.GallerySource{{Item: capsule.GalleryItem{Name: "a.jpg", Size: 3}, Original: source}})
				} else {
					_, err = scoped.MintStream(rec, "", source)
				}
				if err == nil {
					t.Fatal("revoked device published a capsule")
				}
				return capsule.MobileResult{Status: 401, Body: []byte(`{"error":"authentication required"}`)}
			})
			if err != nil || got.Status != 401 || len(h.caps.ListOwner(device.OwnerID)) != 0 {
				t.Fatalf("publication escaped grant barrier: %v", err)
			}
			// Retrying the interrupted/denied operation cannot re-admit storage work.
			_, err = h.caps.MobileMint(res.Vault, device.OwnerID, "revoked", []byte("spec"), "https://grab.test", 200, nil, func(*capsule.Store) capsule.MobileResult {
				t.Fatal("denied operation readmitted")
				return capsule.MobileResult{}
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMobileGrabGrantWatcherCancelsAndVaultLockCancels(t *testing.T) {
	for _, reason := range []string{"revoke", "lock"} {
		t.Run(reason, func(t *testing.T) {
			h, device, token := mobileGrantFixture(t, []string{users.GrabsWrite, users.FilesRead})
			res := h.registry.For(h.users.Users()[0])
			r := httptest.NewRequest("POST", "/api/capsules", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			request, publish, release, err := h.mobileGrabAuthorization(r, res.Vault)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if reason == "revoke" {
				if err := h.users.RevokeDevice(device.OwnerID, device.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				res.Vault.Lock()
			}
			select {
			case <-request.Context().Done():
			case <-time.After(2 * time.Second):
				t.Fatal("native source was not canceled")
			}
			committed := false
			err = publish(func() error { committed = true; return nil })
			if err == nil || committed {
				t.Fatal("canceled source passed final publication")
			}
		})
	}
}

func TestMobileGrabGrantRequiresSourceRead(t *testing.T) {
	h, _, token := mobileGrantFixture(t, []string{users.GrabsWrite})
	res := h.registry.For(h.users.Users()[0])
	r := httptest.NewRequest("POST", "/api/capsules", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	_, _, release, err := h.mobileGrabAuthorization(r, res.Vault)
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("missing source read accepted")
	}
}
