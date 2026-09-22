package accountlifecycle

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestDeleteResumesAndOnlyRemovesOwnerData(t *testing.T) {
	root := t.TempDir()
	us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := us.Create("alice", "alice-password", false)
	bob, _ := us.Create("bob", "bob-password", false)
	bobData := filepath.Join(root, "users", bob.ID, "places.json")
	bobBytes := []byte("bob-only-settings")
	if err := os.WriteFile(bobData, bobBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	bobHash := sha256.Sum256(bobBytes)
	capsRoot := filepath.Join(root, "capsules")
	caps := capsule.New(capsRoot)
	a, err := caps.Mint(capsule.Record{Owner: alice.ID, Name: "a.txt", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 3}, "", []byte("alice"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := caps.Mint(capsule.Record{Owner: bob.ID, Name: "b.txt", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 3}, "", []byte("bob"))
	if err != nil {
		t.Fatal(err)
	}
	quotaManager := quota.New(root)
	registry := filesvc.NewRegistry(us, quotaManager)
	uploads := upload.New(filepath.Join(root, "uploads"), registry.For, quotaManager, us.Count)
	if _, err := uploads.Create(alice, "alice.iso", 20, ""); err != nil {
		t.Fatal(err)
	}
	bobUpload, err := uploads.Create(bob, "bob.iso", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	m := New(us, registry, caps, uploads)
	broken := filepath.Join(capsRoot, "00000000000000000000000000000000")
	if err := os.Mkdir(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "meta.json"), []byte("broken metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(context.Background(), alice.ID); err == nil {
		t.Fatal("expected deletion to remain pending while capsule storage is unavailable")
	}
	if u, ok := us.User(alice.ID); !ok || !u.Deleting || !u.Disabled {
		t.Fatal("failed deletion did not retain a disabled tombstone")
	}
	if err := os.RemoveAll(broken); err != nil {
		t.Fatal(err)
	}
	restarted, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	restartedQuota := quota.New(root)
	restartedRegistry := filesvc.NewRegistry(restarted, restartedQuota)
	restartedUploads := upload.New(filepath.Join(root, "uploads"), restartedRegistry.For, restartedQuota, restarted.Count)
	restartedManager := New(restarted, restartedRegistry, caps, restartedUploads)
	if err := restartedManager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.User(alice.ID); ok {
		t.Fatal("completed deletion left account record")
	}
	if _, err := os.Stat(filepath.Join(root, "users", alice.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted user data still exists")
	}
	if _, err := os.Stat(filepath.Join(root, "uploads", alice.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted upload state still exists")
	}
	if _, err := restartedUploads.Status(bob, bobUpload.ID); err != nil {
		t.Fatal("other user's upload state changed:", err)
	}
	if _, ok := restarted.User(bob.ID); !ok {
		t.Fatal("deleting alice removed bob's account")
	}
	remaining, err := os.ReadFile(bobData)
	if err != nil || sha256.Sum256(remaining) != bobHash {
		t.Fatal("another user's data changed during deletion")
	}
	if _, _, err := caps.Grab(a.ID, ""); err == nil {
		t.Fatal("deleted owner's capsule still works")
	}
	if body, _, err := caps.Grab(b.ID, ""); err != nil || string(body) != "bob" {
		t.Fatalf("other owner's capsule changed: %q %v", body, err)
	}
}

func TestDisableRevokesSessionsAndGrabLinksThenResumesMarker(t *testing.T) {
	root := t.TempDir()
	us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := us.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	caps := capsule.New(filepath.Join(root, "capsules"))
	rec, err := caps.Mint(capsule.Record{Owner: u.ID, Name: "a.txt", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 5}, "", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	q := quota.New(root)
	registry := filesvc.NewRegistry(us, q)
	uploads := upload.New(filepath.Join(root, "uploads"), registry.For, q, us.Count)
	m := New(us, registry, caps, uploads)
	if err := us.SetDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, ok := us.User(u.ID); !ok || !got.DisablePending {
		t.Fatal("disable recovery marker was not persisted")
	}
	restarted, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
	if err != nil {
		t.Fatal(err)
	}
	m = New(restarted, filesvc.NewRegistry(restarted, q), caps, uploads)
	if err := m.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Current(requestWithSession(token)); err == nil {
		t.Fatal("disabled user's session survived")
	}
	if _, _, err := caps.Grab(rec.ID, ""); err == nil {
		t.Fatal("disabled user's grab link remained active")
	}
	got, _ := restarted.User(u.ID)
	if !got.Disabled || got.DisablePending {
		t.Fatalf("disable state=%+v", got)
	}
}

func requestWithSession(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "weazl_session", Value: token})
	return r
}

func TestDeleteReplaysAfterEveryCleanupBoundary(t *testing.T) {
	for _, boundary := range []string{"marked", "uploads removed", "capsules removed", "user directory removed"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
			if err != nil {
				t.Fatal(err)
			}
			alice, _ := us.Create("alice", "alice-password", false)
			bob, _ := us.Create("bob", "bob-password", false)
			bobPath := filepath.Join(root, "users", bob.ID, "places.json")
			body := []byte("keep bob")
			if err := os.WriteFile(bobPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(body)
			caps := capsule.New(filepath.Join(root, "capsules"))
			for _, owner := range []users.User{alice, bob} {
				if _, err := caps.Mint(capsule.Record{Owner: owner.ID, Name: "owned.txt", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 4}, "", []byte(owner.Username)); err != nil {
					t.Fatal(err)
				}
			}
			q := quota.New(root)
			reg := filesvc.NewRegistry(us, q)
			uploads := upload.New(filepath.Join(root, "uploads"), reg.For, q, us.Count)
			for _, owner := range []users.User{alice, bob} {
				if _, err := uploads.Create(owner, owner.Username+".iso", 12, ""); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := us.BeginDelete(alice.ID); err != nil {
				t.Fatal(err)
			}
			if boundary != "marked" {
				if err := uploads.DeleteOwner(alice.ID); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "capsules removed" || boundary == "user directory removed" {
				if err := caps.DeleteOwner(alice.ID); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "user directory removed" {
				p, err := us.DataPath(alice)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(p); err != nil {
					t.Fatal(err)
				}
			}
			// Construct fresh stores/managers to model a node restart at this boundary.
			restarted, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
			if err != nil {
				t.Fatal(err)
			}
			rq := quota.New(root)
			rr := filesvc.NewRegistry(restarted, rq)
			ru := upload.New(filepath.Join(root, "uploads"), rr.For, rq, restarted.Count)
			rm := New(restarted, rr, caps, ru)
			if err := rm.ResumePending(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, ok := restarted.User(alice.ID); ok {
				t.Fatal("deletion did not converge")
			}
			if _, ok := restarted.User(bob.ID); !ok {
				t.Fatal("other account was removed")
			}
			remaining, err := os.ReadFile(bobPath)
			if err != nil || sha256.Sum256(remaining) != hash {
				t.Fatal("other user's data changed")
			}
			if _, err := ru.List(bob); err != nil {
				t.Fatal("other user's upload state changed:", err)
			}
		})
	}
}
