package mobileparts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

type batchTemporaryFailure struct{}

func (batchTemporaryFailure) Error() string   { return "batch writer interrupted" }
func (batchTemporaryFailure) Retryable() bool { return true }

func TestBatchRetrySurvivesRestartAndStopsAfterBound(t *testing.T) {
	m, res, q := fixture(t)
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	data := []byte("durable original remains available throughout retry")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	authorize := func(Session) error { return nil }
	calls := 0
	fail := func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		calls++
		return nil, batchTemporaryFailure{}
	}
	for attempt := 1; attempt <= 4; attempt++ {
		if err := m.Process(context.Background(), res, sessionID, authorize, fail); err == nil {
			t.Fatal("missing injected failure")
		}
		s := diskSession(t, res)
		if attempt == 4 {
			if s.Status != "failed" || s.RetryAttempts != 3 {
				t.Fatalf("retry bound: %+v", s)
			}
			break
		}
		if s.Status != "queued" || s.RetryAttempts != attempt || !s.RetryAfter.Equal(now.Add(time.Second*time.Duration(1<<attempt))) {
			t.Fatalf("retry state: %+v", s)
		}
		if err := m.ReleaseOwner(owner); err != nil {
			t.Fatal(err)
		}
		m = New(q)
		m.now = func() time.Time { return now }
		pending, err := m.Pending(res)
		if err != nil || len(pending) != 0 {
			t.Fatalf("backoff ignored after restart: %d %v", len(pending), err)
		}
		if err = m.Process(context.Background(), res, sessionID, authorize, fail); !errors.Is(err, ErrConflict) {
			t.Fatalf("early dispatch: %v", err)
		}
		now = s.RetryAfter
	}
	defer m.ReleaseOwner(owner)
	if calls != 4 {
		t.Fatal("unexpected attempts", calls)
	}
	r, err := m.Open(context.Background(), res, sessionID, "original")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(body) != string(data) {
		t.Fatal("failed batch lost bytes", err)
	}
	if _, err = m.Retry(res, sessionID, device); err != nil {
		t.Fatal(err)
	}
	s := diskSession(t, res)
	if s.RetryAttempts != 0 || !s.RetryAfter.IsZero() {
		t.Fatal("explicit retry retained backoff")
	}
	if err = m.Process(context.Background(), res, sessionID, authorize, func(Session, func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"stored"}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	noPayload(t, res)
}
