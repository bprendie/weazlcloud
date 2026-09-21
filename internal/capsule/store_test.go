package capsule

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamRoundTripAndLegacyReader(t *testing.T) {
	s := New(t.TempDir())
	payload := bytes.Repeat([]byte("iso-block-"), 350000)
	rec, err := s.MintStream(Record{Name: "disk.iso", Kind: "file", Gate: "passphrase", Expires: time.Now().Add(time.Hour), Limit: 2}, "nug", func(w io.Writer) error {
		_, err := w.Write(payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StreamGrab(rec.ID, "wrong", func(Record) (io.Writer, error) { return io.Discard, nil }); err != ErrPhrase {
		t.Fatalf("wrong stream phrase %v", err)
	}
	var streamed bytes.Buffer
	got, err := s.StreamGrab(rec.ID, "nug", func(got Record) (io.Writer, error) {
		if got.Used != 1 {
			t.Fatalf("used=%d", got.Used)
		}
		return &streamed, nil
	})
	if err != nil || got.Used != 1 || !bytes.Equal(streamed.Bytes(), payload) {
		t.Fatalf("stream grab err=%v used=%d size=%d", err, got.Used, streamed.Len())
	}
	if _, err := s.StreamGrab(rec.ID, "nug", func(Record) (io.Writer, error) { return failingWriter{}, nil }); err == nil {
		t.Fatal("interrupted stream succeeded")
	}
	legacy, err := s.Mint(Record{Name: "old.bin", Kind: "file", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 1}, "", []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := s.Grab(legacy.ID, "")
	if err != nil || string(old) != "old" {
		t.Fatalf("legacy reader err=%v body=%q", err, old)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("interrupted") }

func TestOpenGrabBurns(t *testing.T) {
	s := New(t.TempDir())
	rec := Record{Label: "recipient", Name: "nug.md", Kind: "file", Gate: "open", Expires: time.Now().Add(time.Hour), Limit: 1, Size: 4}
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
