package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
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
	Jobs  []MediaJob `json:"jobs"`
	index *jobIndex
}

func NewMediaJob(ownerID, assetID string, revision uint64, operation, renderer string, priority int) MediaJob {
	key := ownerID + "\x00" + assetID + "\x00" + strconv.FormatUint(revision, 10) + "\x00" + operation + "\x00" + renderer
	sum := sha256.Sum256([]byte(key))
	return MediaJob{ID: hex.EncodeToString(sum[:16]), OwnerID: ownerID, AssetID: assetID, Revision: revision, Operation: operation, Renderer: renderer, Priority: priority, Status: JobPending}
}

func (q *JobQueue) Upsert(job MediaJob) bool {
	if old, ok := q.Get(job.ID); ok {
		if job.Priority < old.Priority {
			old.Priority = job.Priority
			q.Replace(old)
		}
		return false
	}
	q.Replace(job)
	return true
}

func (q *JobQueue) Lease(owner string, now time.Time, duration time.Duration, limit int) []MediaJob {
	if limit < 1 || owner == "" {
		return nil
	}
	q.ensureIndex()
	q.makeDue(now)
	leased := make([]MediaJob, 0, min(limit, len(q.index.ready.items)))
	for len(leased) < limit && len(q.index.ready.items) > 0 {
		job := q.index.ready.items[0].job
		job.Status, job.LeaseOwner, job.LeaseUntil, job.Progress = JobLeased, owner, now.Add(duration), 0
		q.Replace(job)
		leased = append(leased, job)
	}
	return leased
}

func (q *JobQueue) Renew(id, owner string, until time.Time) bool {
	job, ok := q.owned(id, owner)
	if !ok {
		return false
	}
	job.LeaseUntil = until
	q.Replace(job)
	return true
}
func (q *JobQueue) Complete(id, owner string) bool {
	job, ok := q.owned(id, owner)
	if !ok {
		return false
	}
	job.Status, job.Progress, job.LeaseOwner, job.LeaseUntil, job.ErrorCategory = JobSucceeded, 100, "", time.Time{}, ""
	q.Replace(job)
	return true
}
func (q *JobQueue) Release(id, owner string) bool {
	job, ok := q.owned(id, owner)
	if !ok {
		return false
	}
	job.Status, job.LeaseOwner, job.LeaseUntil, job.Progress = JobPending, "", time.Time{}, 0
	q.Replace(job)
	return true
}
func (q *JobQueue) Fail(id, owner, category string, retryable bool, now time.Time) bool {
	job, ok := q.owned(id, owner)
	if !ok {
		return false
	}
	job.Attempts++
	job.Progress, job.ErrorCategory = 0, category
	job.LeaseOwner, job.LeaseUntil = "", time.Time{}
	if retryable && job.Attempts < MaxJobAttempts {
		job.Status, job.NextAttemptAt = JobPending, now.Add(time.Second*time.Duration(1<<min(job.Attempts-1, 8)))
	} else {
		job.Status = JobFailed
	}
	q.Replace(job)
	return true
}
func (q *JobQueue) CancelOwner(owner string) int {
	count := 0
	for _, job := range q.Jobs {
		if job.OwnerID == owner && (job.Status == JobPending || job.Status == JobLeased) {
			job.Status, job.LeaseOwner, job.LeaseUntil = JobCanceled, "", time.Time{}
			q.Replace(job)
			count++
		}
	}
	return count
}
func (q *JobQueue) RequeueLeasesExcept(owner string) int {
	q.ensureIndex()
	jobs := make([]MediaJob, 0, len(q.index.leased.items))
	for _, entry := range q.index.leased.items {
		if entry.job.LeaseOwner != owner {
			jobs = append(jobs, entry.job)
		}
	}
	for _, job := range jobs {
		q.Release(job.ID, job.LeaseOwner)
	}
	return len(jobs)
}
func (q *JobQueue) Counts() (pending, leased, succeeded, failed int) {
	q.ensureIndex()
	c := q.index.counts
	return c[JobPending], c[JobLeased], c[JobSucceeded], c[JobFailed]
}
func (q *JobQueue) HasJobs() bool { return len(q.Jobs) > 0 }
func (q *JobQueue) owned(id, owner string) (MediaJob, bool) {
	job, ok := q.Get(id)
	return job, ok && job.Status == JobLeased && job.LeaseOwner == owner
}
