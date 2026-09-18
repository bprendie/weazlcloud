package capsule

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenGrabBurns(t *testing.T) {
	s := New(t.TempDir())
	rec := Record{Label: "Gil", Name: "nug.md", Kind: "file", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 1, Size: 4}
	got, err := s.Mint(rec, "", []byte("nug!"))
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := s.Grab(got.ID, "")
	if err != nil || !bytes.Equal(body, []byte("nug!")) {
		t.Fatalf("grab %v %q", err, body)
	}
	if _, _, err := s.Grab(got.ID, ""); err != ErrGone {
		t.Fatalf("second grab %v", err)
	}
}

func TestPassphraseAndRevoke(t *testing.T) {
	s := New(t.TempDir())
	rec := Record{Name: "secret.md", Kind: "file", Gate: "passphrase", Expires: time.Now().Add(time.Hour), Limit: 5, Size: 3}
	got, err := s.Mint(rec, "nug", []byte("xyz"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Grab(got.ID, "nope"); err != ErrPhrase {
		t.Fatalf("wrong phrase %v", err)
	}
	body, _, err := s.Grab(got.ID, "nug")
	if err != nil || !bytes.Equal(body, []byte("xyz")) {
		t.Fatalf("phrase grab %v %q", err, body)
	}
	if err := s.Revoke(got.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Grab(got.ID, "nug"); err != ErrGone {
		t.Fatalf("revoked %v", err)
	}
	_ = filepath.Separator
}

func TestExpired(t *testing.T) {
	s := New(t.TempDir())
	rec := Record{Name: "old.md", Kind: "file", Gate: "open", Expires: time.Now().Add(-time.Minute), Limit: 3, Size: 1}
	got, err := s.Mint(rec, "", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Grab(got.ID, ""); err != ErrGone {
		t.Fatalf("expired %v", err)
	}
}
