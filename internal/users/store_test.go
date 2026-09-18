package users

import (
	"path/filepath"
	"testing"
)

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
