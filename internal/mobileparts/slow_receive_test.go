package mobileparts

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type heldReceive struct {
	entered, release chan struct{}
	body             io.Reader
	started          bool
}

func (r *heldReceive) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		close(r.entered)
		<-r.release
	}
	return r.body.Read(p)
}

func TestSlowReceiveDoesNotBlockPendingOrSweep(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		name := "first-index"
		if indexed {
			name = "indexed"
		}
		t.Run(name, func(t *testing.T) {
			m, res, _ := fixture(t)
			data := []byte("complete original")
			create(t, m, res, data)
			appendPart(t, m, res, data, 0)
			if indexed {
				if _, err := m.Pending(res); err != nil {
					t.Fatal(err)
				}
			}
			slowID := strings.Repeat("d", 32)
			if _, err := m.Create(res, owner, slowID, specFor(data)); err != nil {
				t.Fatal(err)
			}
			r := &heldReceive{entered: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(data)}
			appendDone := make(chan error, 1)
			go func() {
				_, err := m.Append(context.Background(), res, slowID, device, "original", 0, int64(len(data)), digest(data), r)
				appendDone <- err
			}()
			<-r.entered
			done := make(chan error, 1)
			collected := false
			defer func() {
				close(r.release)
				if err := <-appendDone; err != nil {
					t.Error(err)
				}
				if !collected {
					<-done
				}
			}()
			go func() {
				pending, err := m.Pending(res)
				if err == nil && (len(pending) != 1 || pending[0].ID != sessionID) {
					t.Errorf("ready peer not returned: %v", pending)
				}
				if err == nil {
					err = m.Sweep(res)
				}
				done <- err
			}()
			select {
			case err := <-done:
				collected = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Error("queue scan waited for a stalled network receive")
			}
		})
	}
}

func TestBusyLegacyIndexRetainsRecoveryMarker(t *testing.T) {
	m, res, _ := fixture(t)
	data := []byte("legacy ready original")
	create(t, m, res, data)
	appendPart(t, m, res, data, 0)
	if err := dropSessionMarkers(res, sessionID); err != nil {
		t.Fatal(err)
	}
	unlock := m.lock(res, sessionID)
	if err := m.recoverIndex(res); err != nil {
		unlock()
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(Root(res), ".live", sessionID))
	unlock()
	if err != nil {
		t.Fatal("busy legacy session lost its recovery marker", err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		pending, err := m.Pending(res)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 && pending[0].ID == sessionID {
			return
		}
	}
	t.Fatal("legacy work lost after bounded index recovery rounds")
}
