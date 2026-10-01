package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

const (
	JobPending     = "pending"
	JobLeased      = "leased"
	JobSucceeded   = "succeeded"
	JobFailed      = "failed"
	JobCanceled    = "canceled"
	MaxJobAttempts = 5
)

type MediaJob struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	AssetID       string    `json:"asset_id"`
	Revision      uint64    `json:"revision"`
	Operation     string    `json:"operation"`
	Renderer      string    `json:"renderer"`
	Priority      int       `json:"priority"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	Progress      int       `json:"progress"`
	LeaseOwner    string    `json:"lease_owner,omitempty"`
	LeaseUntil    time.Time `json:"lease_until,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	ErrorCategory string    `json:"error_category,omitempty"`
}

type JobQueue struct {
	Jobs []MediaJob `json:"jobs"`
}

func NewMediaJob(ownerID, assetID string, revision uint64, operation, renderer string, priority int) MediaJob {
	key := ownerID + "\x00" + assetID + "\x00" + uintString(revision) + "\x00" + operation + "\x00" + renderer
	sum := sha256.Sum256([]byte(key))
	return MediaJob{ID: hex.EncodeToString(sum[:16]), OwnerID: ownerID, AssetID: assetID, Revision: revision, Operation: operation, Renderer: renderer, Priority: priority, Status: JobPending}
}

func (q *JobQueue) Upsert(job MediaJob) bool {
	for i := range q.Jobs {
		old := &q.Jobs[i]
		if old.ID != job.ID {
			continue
		}
		if job.Priority < old.Priority {
			old.Priority = job.Priority
		}
		return false
	}
	q.Jobs = append(q.Jobs, job)
	return true
}

func (q *JobQueue) Lease(owner string, now time.Time, duration time.Duration, limit int) []MediaJob {
	if limit < 1 || owner == "" {
		return nil
	}
	ready := make([]int, 0)
	for i := range q.Jobs {
		job := &q.Jobs[i]
		if job.Status == JobLeased && !job.LeaseUntil.After(now) {
			job.Status, job.LeaseOwner, job.LeaseUntil = JobPending, "", time.Time{}
		}
		if job.Status == JobPending && !job.NextAttemptAt.After(now) && job.Attempts < MaxJobAttempts {
			ready = append(ready, i)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		a, b := q.Jobs[ready[i]], q.Jobs[ready[j]]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if !a.NextAttemptAt.Equal(b.NextAttemptAt) {
			return a.NextAttemptAt.Before(b.NextAttemptAt)
		}
		return a.ID < b.ID
	})
	if len(ready) > limit {
		ready = ready[:limit]
	}
	leased := make([]MediaJob, 0, len(ready))
	for _, index := range ready {
		job := &q.Jobs[index]
		job.Status, job.LeaseOwner = JobLeased, owner
		job.LeaseUntil = now.Add(duration)
		job.Progress = 0
		leased = append(leased, *job)
	}
	return leased
}

func (q *JobQueue) Renew(jobID, owner string, until time.Time) bool {
	job := q.find(jobID, owner)
	if job == nil {
		return false
	}
	job.LeaseUntil = until
	return true
}

func (q *JobQueue) Complete(jobID, owner string) bool {
	job := q.find(jobID, owner)
	if job == nil {
		return false
	}
	job.Status, job.Progress = JobSucceeded, 100
	job.LeaseOwner, job.LeaseUntil = "", time.Time{}
	job.ErrorCategory = ""
	return true
}

func (q *JobQueue) Release(jobID, owner string) bool {
	job := q.find(jobID, owner)
	if job == nil {
		return false
	}
	job.Status, job.LeaseOwner, job.LeaseUntil, job.Progress = JobPending, "", time.Time{}, 0
	return true
}

func (q *JobQueue) Fail(jobID, owner, category string, retryable bool, now time.Time) bool {
	job := q.find(jobID, owner)
	if job == nil {
		return false
	}
	job.Attempts++
	job.Progress, job.ErrorCategory = 0, category
	job.LeaseOwner, job.LeaseUntil = "", time.Time{}
	if retryable && job.Attempts < MaxJobAttempts {
		delay := time.Second * time.Duration(1<<min(job.Attempts-1, 8))
		job.Status, job.NextAttemptAt = JobPending, now.Add(delay)
	} else {
		job.Status = JobFailed
	}
	return true
}

func (q *JobQueue) CancelOwner(ownerID string) int {
	changed := 0
	for i := range q.Jobs {
		job := &q.Jobs[i]
		if job.OwnerID == ownerID && (job.Status == JobPending || job.Status == JobLeased) {
			job.Status, job.LeaseOwner, job.LeaseUntil = JobCanceled, "", time.Time{}
			changed++
		}
	}
	return changed
}

func (q *JobQueue) RequeueLeasesExcept(owner string) int {
	changed := 0
	for i := range q.Jobs {
		job := &q.Jobs[i]
		if job.Status == JobLeased && job.LeaseOwner != owner {
			job.Status, job.LeaseOwner, job.LeaseUntil, job.Progress = JobPending, "", time.Time{}, 0
			changed++
		}
	}
	return changed
}

func (q *JobQueue) Counts() (pending, leased, succeeded, failed int) {
	for _, job := range q.Jobs {
		switch job.Status {
		case JobPending:
			pending++
		case JobLeased:
			leased++
		case JobSucceeded:
			succeeded++
		case JobFailed:
			failed++
		}
	}
	return
}

func (q *JobQueue) HasJobs() bool { return len(q.Jobs) > 0 }

func (q *JobQueue) find(id, owner string) *MediaJob {
	for i := range q.Jobs {
		if q.Jobs[i].ID == id && q.Jobs[i].Status == JobLeased && q.Jobs[i].LeaseOwner == owner {
			return &q.Jobs[i]
		}
	}
	return nil
}

func uintString(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
