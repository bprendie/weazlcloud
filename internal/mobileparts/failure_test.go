package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

type codedFailure struct {
	code  string
	cause error
}

func (e codedFailure) Error() string       { return e.code }
func (e codedFailure) FailureCode() string { return e.code }
func (e codedFailure) Unwrap() error       { return e.cause }

func TestFailureCodeAndFreshGrantAutoQueue(t *testing.T) {
	m, res, _ := fixture(t)
	create(t, m, res, nil)
	authorize := func(Session) error { return nil }
	commit := func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		return nil, fmt.Errorf("publish: %w", codedFailure{"stale_revision", errors.New("stale")})
	}
	if err := m.Process(context.Background(), res, sessionID, authorize, commit); err == nil {
		t.Fatal("expected commit failure")
	}
	s := diskSession(t, res)
	if s.Status != "failed" || s.ErrorCode != "stale_revision" {
		t.Fatalf("truthful failure code: %+v", s)
	}
	if err := m.RebindPayload(res, sessionID, device, s.Spec.Payload, json.RawMessage(`{"grant":"fresh"}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := m.Pending(res)
	if err != nil || len(pending) != 1 || pending[0].Status != "queued" || pending[0].ErrorCode != "" {
		t.Fatalf("fresh admitted grant did not requeue: %v %v", pending, err)
	}
	if got := failureCode(errors.New("unknown")); got != "commit_failed" {
		t.Fatalf("generic code: %s", got)
	}
	if got := failureCode(codedFailure{"", nil}); got != "commit_failed" {
		t.Fatalf("empty code: %s", got)
	}
	checksum := func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		return nil, codedFailure{"stale_revision", ErrChecksum}
	}
	if err = m.Process(context.Background(), res, sessionID, authorize, checksum); !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum: %v", err)
	}
	if got := diskSession(t, res).ErrorCode; got != "checksum_mismatch" {
		t.Fatalf("checksum override: %s", got)
	}
}
