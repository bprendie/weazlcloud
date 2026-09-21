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
