package users

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionsExpireAndPasswordChangeInvalidatesThem(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	if _, err := s.Current(req); err != nil {
		t.Fatal("fresh session rejected", err)
	}
	s.mu.Lock()
	s.sessions[token] = session{userID: u.ID, expires: time.Now().Add(-time.Minute)}
	s.mu.Unlock()
	if _, err := s.Current(req); err == nil {
		t.Fatal("expired session accepted")
	}
	token, err = s.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	if err := s.ChangePassword(u.ID, "alice-password", "new-alice-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Current(req); err == nil {
		t.Fatal("password change left old session active")
	}
	var rec httptest.ResponseRecorder
	s.SetSecureCookies(true)
	s.SetSession(&rec, token)
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Fatal("secure cookie mode did not set Secure")
	}
}

func TestAccessRequestRequiresApprovalBeforeAccountCreation(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.RequestAccess("alice", "home node")
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 || q.Status != "pending" {
		t.Fatalf("request created an account: count=%d status=%s", s.Count(), q.Status)
	}
	approved, token, err := s.ApproveAccess(q.ID)
	if err != nil || approved.Status != "approved" || token == "" {
		t.Fatalf("approve: %#v %q %v", approved, token, err)
	}
	if _, err := s.CompleteAccess(q.ID, "wrong", "alice", "alice-password"); err == nil {
		t.Fatal("wrong setup token accepted")
	}
	u, err := s.CompleteAccess(q.ID, token, "alice", "alice-password")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" || s.Count() != 1 {
		t.Fatalf("account was not created: %#v count=%d", u, s.Count())
	}
	if _, err := s.CompleteAccess(q.ID, token, "alice", "alice-password"); err == nil {
		t.Fatal("setup token reused")
	}
}

func TestDisableRevokesSessionsAndProtectsLastAdmin(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.Create("admin", "admin-password", true)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Login(admin)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	if err := s.SetDisabled(admin.ID, true); err == nil {
		t.Fatal("disabled the last active administrator")
	}
	user, err := s.Create("alice", "alice-password", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err = s.Login(user)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	if err := s.SetDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(user); err == nil {
		t.Fatal("created a new session from a stale enabled account")
	}
	if err := s.SetDisableError(user.ID, "grab links"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(user.ID, false); err == nil {
		t.Fatal("reenabled an account before disable cleanup completed")
	}
	if err := s.SetDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisableError(user.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Current(req); err == nil {
		t.Fatal("session remained valid after disabling the account")
	}
	loaded, err := New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := loaded.User(user.ID); !ok || !got.Disabled {
		t.Fatal("disabled state did not survive restart")
	}
}
