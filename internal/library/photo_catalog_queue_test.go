package library

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoCatalogGroupsAuthorizationCancellationAndPublication(t *testing.T) {
	l, _ := componentFixture(t)
	ctx := context.Background()
	denied := errors.New("device revoked")
	var allowedCalls, deniedCalls atomic.Int32
	allowed := func(publish func() error) error {
		if l.mu.TryLock() {
			l.mu.Unlock()
			t.Error("authorization did not hold Library lock")
		}
		allowedCalls.Add(1)
		return publish()
	}
	blocked := func(func() error) error { deniedCalls.Add(1); return denied }
	_, session := l.vault.State()
	requests := []photoCatalogRequest{}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf(".weazl-mobile-pending/%d", i)
		f, err := storeComponent(l, ctx, fmt.Sprint(i), []byte("content"))
		if err != nil {
			t.Fatal(err)
		}
		commit := catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: fmt.Sprint(i), SourceRevision: "1", Files: []catalog.PhotoIngestFile{{ID: "original", From: name, To: fmt.Sprintf("Photos/%d.jpg", i), Hash: f.Hash, Size: f.Size}}}
		r := photoCatalogRequest{ctx: ctx, session: session, commit: commit, group: "allowed", guard: allowed, done: make(chan catalog.PhotoBatchResult, 1)}
		if i == 2 {
			r.group = "revoked"
			r.guard = blocked
		}
		if i == 3 {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			r.ctx = cancelled
		}
		requests = append(requests, r)
	}
	version := l.catalog.Version()
	// Feed a known simultaneous discovery round so group boundaries are deterministic.
	l.photoCatalogMu.Lock()
	l.photoCatalogPending = requests
	l.photoCatalogRunning = true
	l.photoCatalogMu.Unlock()
	go l.runPhotoCatalog()
	for i, r := range requests {
		select {
		case result := <-r.done:
			if i < 2 && result.Err != nil {
				t.Fatal(result.Err)
			}
			if i == 2 && !errors.Is(result.Err, denied) {
				t.Fatal("revoked member published", result.Err)
			}
			if i == 3 && !errors.Is(result.Err, context.Canceled) {
				t.Fatal("cancelled member published", result.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("catalog queue stalled")
		}
	}
	if allowedCalls.Load() != 1 || deniedCalls.Load() != 1 || l.catalog.Version() != version+1 {
		t.Fatal("grants not isolated or writes not coalesced")
	}
	for i := 0; i < 4; i++ {
		_, exists := l.catalog.Get(fmt.Sprintf("Photos/%d.jpg", i))
		if exists != (i < 2) {
			t.Fatal("unexpected publication", i)
		}
	}
	page, err := l.PhotoContentLookup(ctx, []PhotoContentKey{lookupKey([]byte("content"))}, false)
	if err != nil || page.Results[0].MatchCount != 2 {
		t.Fatal("published index not updated", err)
	}
}
