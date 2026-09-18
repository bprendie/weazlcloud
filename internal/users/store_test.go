package users

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLocalUsersAndSessions(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "users.json"), filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Create("alice", "alice-password", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("alice", "wrong-password"); err != ErrBadCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Authenticate("alice", "alice-password"); err != nil {
		t.Fatal(err)
	}
	token, err := s.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	if got, err := s.Current(r); err != nil || got.ID != u.ID {
		t.Fatalf("current user: %+v %v", got, err)
	}
}
