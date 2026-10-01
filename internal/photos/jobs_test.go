package photos

import (
	"testing"
	"time"
)

func TestMediaJobIdentityAndPriorityLeasing(t *testing.T) {
	now := time.Now().UTC()
	background := NewMediaJob("owner", "asset-a", 1, "thumbnail", "r1", 3)
	visible := NewMediaJob("owner", "asset-b", 1, "thumbnail", "r1", 0)
	if background.ID != NewMediaJob("owner", "asset-a", 1, "thumbnail", "r1", 1).ID || background.ID == NewMediaJob("owner", "asset-a", 2, "thumbnail", "r1", 3).ID {
		t.Fatal("job identity is not stable across priority or distinct across revision")
	}
	queue := JobQueue{}
	queue.Upsert(background)
	queue.Upsert(visible)
	leased := queue.Lease("worker-a", now, time.Minute, 1)
	if len(leased) != 1 || leased[0].ID != visible.ID {
		t.Fatalf("lease did not honor visible priority: %+v", leased)
	}
	if !queue.Complete(visible.ID, "worker-a") {
		t.Fatal("valid lease could not complete")
	}
	if queue.Complete(background.ID, "worker-a") {
		t.Fatal("another worker's job was completed")
	}
}

func TestMediaJobRecoveryBackoffAndTerminalFailure(t *testing.T) {
	now := time.Now().UTC()
	job := NewMediaJob("owner", "asset", 4, "thumbnail", "r1", 1)
	queue := JobQueue{Jobs: []MediaJob{job}}
	if got := queue.Lease("worker-a", now, time.Minute, 1); len(got) != 1 {
		t.Fatalf("initial lease=%+v", got)
	}
	if got := queue.Lease("worker-b", now.Add(2*time.Minute), time.Minute, 1); len(got) != 1 || got[0].LeaseOwner != "worker-b" {
		t.Fatalf("expired lease was not reclaimed: %+v", got)
	}
	if !queue.Fail(job.ID, "worker-b", "corrupt_media", true, now) {
		t.Fatal("leased job did not accept failure")
	}
	if got := queue.Lease("worker-c", now.Add(500*time.Millisecond), time.Minute, 1); len(got) != 0 {
		t.Fatalf("backoff was ignored: %+v", got)
	}
	for attempt := 1; attempt < MaxJobAttempts; attempt++ {
		leased := queue.Lease("worker-c", now.Add(time.Duration(1<<attempt)*time.Second), time.Minute, 1)
		if len(leased) != 1 || !queue.Fail(job.ID, "worker-c", "corrupt_media", true, now) {
			t.Fatalf("attempt %d was not retried", attempt+1)
		}
	}
	_, _, _, failed := queue.Counts()
	if failed != 1 || queue.Jobs[0].Attempts != MaxJobAttempts {
		t.Fatalf("job did not reach bounded terminal failure: %+v", queue.Jobs[0])
	}
}

func TestMediaJobUpsertAndLeaseRecovery(t *testing.T) {
	now := time.Now().UTC()
	queue := JobQueue{}
	job := NewMediaJob("owner", "asset", 1, "thumbnail", "r1", 5)
	if !queue.Upsert(job) || queue.Upsert(NewMediaJob("owner", "asset", 1, "thumbnail", "r1", 0)) {
		t.Fatal("upsert did not deduplicate stable job key")
	}
	if queue.Jobs[0].Priority != 0 {
		t.Fatalf("priority was not promoted: %+v", queue.Jobs[0])
	}
	queue.Lease("old-worker", now, time.Hour, 1)
	if n := queue.RequeueLeasesExcept("new-worker"); n != 1 || queue.Jobs[0].Status != JobPending {
		t.Fatalf("old worker lease was not recovered: %+v n=%d", queue.Jobs[0], n)
	}
}
