package idle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestCoordinatorWaitsForQuietAndIgnoresProbesAndSSE(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	c := New(10*time.Second, func() time.Time { return now })
	runs := 0
	c.Register(func(context.Context) error { runs++; return nil })
	for _, path := range []string{"/live", "/ready", "/api/library/events"} {
		if TracksRequest(path) {
			t.Fatalf("%s should not prevent idle", path)
		}
	}
	if !TracksRequest("/api/library?path=file") || !TracksRequest("/dav/") {
		t.Fatal("storage requests are not tracked")
	}
	now = now.Add(9 * time.Second)
	if c.RunOnce(context.Background()) || runs != 0 {
		t.Fatal("maintenance started before the quiet interval")
	}
	now = now.Add(time.Second)
	if !c.RunOnce(context.Background()) || runs != 1 {
		t.Fatal("maintenance did not start after the quiet interval")
	}
}

func TestMaintenanceStatusPersistsSafeResultAndReloads(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	statusPath := filepath.Join(t.TempDir(), "maintenance.json")
	c := New(time.Second, func() time.Time { return now })
	if err := c.SetStatusPath(statusPath); err != nil {
		t.Fatal(err)
	}
	c.RegisterTask("cache-cleanup", func(context.Context) (int64, error) { return 4321, syscall.ENOSPC })
	now = now.Add(2 * time.Second)
	if !c.RunOnce(context.Background()) {
		t.Fatal("maintenance did not run")
	}
	jobs := c.Status()
	if len(jobs) != 1 || jobs[0].LastResult != "failed" || jobs[0].ErrorCategory != "no_space" || jobs[0].ReclaimedBytes != 4321 {
		t.Fatalf("unexpected status: %+v", jobs)
	}
	b, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "no space left") || strings.Contains(string(b), statusPath) {
		t.Fatalf("status leaked raw error details: %s", b)
	}
	var saved []JobStatus
	if err := json.Unmarshal(b, &saved); err != nil || len(saved) != 1 {
		t.Fatalf("saved status=%s err=%v", b, err)
	}
	reloaded := New(time.Second, func() time.Time { return now })
	reloaded.RegisterTask("cache-cleanup", func(context.Context) (int64, error) { return 0, nil })
	if err := reloaded.SetStatusPath(statusPath); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Status()[0]; got.LastResult != "failed" || got.ErrorCategory != "no_space" || got.ReclaimedBytes != 4321 {
		t.Fatalf("reloaded status: %+v", got)
	}
}

func TestErrorCategoryHidesRawPermissionDetails(t *testing.T) {
	if got := ErrorCategory(errors.Join(os.ErrPermission, errors.New("/srv/private/file"))); got != "permission" {
		t.Fatalf("permission category=%q", got)
	}
}

func TestActivityCancelsRunningJobAndPreventsOverlap(t *testing.T) {
	now := time.Now()
	c := New(time.Second, func() time.Time { return now })
	now = now.Add(2 * time.Second)
	started := make(chan struct{})
	stopped := make(chan struct{})
	var active, maxActive atomic.Int32
	c.Register(func(ctx context.Context) error {
		current := active.Add(1)
		for old := maxActive.Load(); current > old && !maxActive.CompareAndSwap(old, current); old = maxActive.Load() {
		}
		close(started)
		<-ctx.Done()
		active.Add(-1)
		close(stopped)
		return ctx.Err()
	})
	finished := make(chan struct{})
	go func() { c.RunOnce(context.Background()); close(finished) }()
	<-started
	release := c.Track()
	if c.RunOnce(context.Background()) {
		t.Fatal("maintenance overlapped a foreground operation")
	}
	<-stopped
	release()
	<-finished
	if maxActive.Load() != 1 || c.Active() != 0 {
		t.Fatalf("overlap=%d active=%d", maxActive.Load(), c.Active())
	}
}

func TestShutdownCancelsRunningMaintenance(t *testing.T) {
	now := time.Now()
	c := New(time.Second, func() time.Time { return now })
	now = now.Add(2 * time.Second)
	started := make(chan struct{})
	stopped := make(chan struct{})
	c.Register(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { c.RunOnce(ctx); close(finished) }()
	<-started
	cancel()
	<-stopped
	<-finished
}
