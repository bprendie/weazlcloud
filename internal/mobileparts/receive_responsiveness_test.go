package mobileparts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSlowOriginalAllowsStatusAndMotion(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("paired original and motion")
	spec := specFor(data)
	spec.Kind = "photo"
	spec.Components = append(spec.Components, Component{ID: "motion", Size: int64(len(data)), SHA256: digest(data)})
	if _, e := m.Create(res, owner, sessionID, spec); e != nil {
		t.Fatal(e)
	}
	r := &heldReceive{entered: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(data)}
	done := make(chan error, 1)
	go func() {
		_, e := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), r)
		done <- e
	}()
	<-r.entered
	defer func() {
		close(r.release)
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	responsive := make(chan error, 1)
	go func() {
		v, e := m.Status(res, sessionID, device)
		if e != nil {
			responsive <- e
			return
		}
		if v.Components[0].ReceivedBytes != 0 {
			responsive <- errors.New("partial bytes counted as durable")
			return
		}
		_, e = m.Append(context.Background(), res, sessionID, device, "motion", 0, int64(len(data)), digest(data), bytes.NewReader(data))
		responsive <- e
	}()
	select {
	case e := <-responsive:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("status/motion blocked by original body")
	}
}
func TestCancelDrainsReceiveAndPreservesNoPayload(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("cancel body")
	create(t, m, res, data)
	pipe, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, e := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), pipe)
		done <- e
	}()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		active := len(m.receivers) > 0
		m.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receive did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- m.Cancel(res, sessionID, device) }()
	select {
	case e := <-cancelled:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not drain body")
	}
	if e := <-done; e == nil {
		t.Fatal("cancelled receiver succeeded")
	}
	noPayload(t, res)
	v, e := m.Status(res, sessionID, device)
	if e != nil || v.Status != "cancelled" {
		t.Fatal(v, e)
	}
}
func TestPendingAdmissionSurvivesRestartAndExistingResume(t *testing.T) {
	m, res, q := fixture(t)
	limits := ReceiveLimits{Global: 4, PerOwner: 2, Pending: 1, PendingBytes: 1024}
	m.ConfigureReceive(limits)
	data := []byte("one accepted upload")
	create(t, m, res, data)
	other := strings.Repeat("d", 32)
	if _, e := m.Create(res, owner, other, specFor(data)); !errors.Is(e, ErrBusy) {
		t.Fatal("new admission bypassed", e)
	}
	restart := New(q)
	restart.ConfigureReceive(limits)
	defer restart.ReleaseOwner(owner)
	if _, e := restart.Create(res, owner, other, specFor(data)); !errors.Is(e, ErrBusy) {
		t.Fatal("restart lost admission", e)
	}
	if _, e := restart.Create(res, owner, sessionID, specFor(data)); e != nil {
		t.Fatal("accepted resume blocked", e)
	}
	appendPart(t, restart, res, data, 0)
	if e := restart.Cancel(res, sessionID, device); e != nil {
		t.Fatal(e)
	}
	if _, e := restart.Create(res, owner, other, specFor(data)); e != nil {
		t.Fatal("terminal upload held admission", e)
	}
}
func TestReceiveGuardDenialDoesNotPublishPart(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("guarded body")
	create(t, m, res, data)
	denied := errors.New("revoked")
	_, e := m.AppendGuarded(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(data), func(func() error) error { return denied })
	if !errors.Is(e, denied) {
		t.Fatal(e)
	}
	v, e := m.Status(res, sessionID, device)
	if e != nil || v.Components[0].ReceivedBytes != 0 {
		t.Fatal("denied bytes published", v, e)
	}
	noPayload(t, res)
}
