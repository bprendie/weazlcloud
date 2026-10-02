package photos

import (
	"container/heap"
	"time"
)

type jobIndex struct {
	byID    map[string]*jobEntry
	ready   jobHeap
	delayed jobHeap
	leased  jobHeap
	counts  map[string]int
	dirty   map[string]bool
}

// Reindex is required after replacing the serialized slice (load/compaction).
// Ordinary mutations use Replace and cost O(log n); progress uses direct lookup.
func (q *JobQueue) Reindex() {
	q.index = &jobIndex{byID: map[string]*jobEntry{}, counts: map[string]int{}, dirty: map[string]bool{}}
	q.index.delayed.kind = 1
	q.index.leased.kind = 2
	for i, job := range q.Jobs {
		q.index.add(&jobEntry{pos: i, job: job, heapIndex: -1})
	}
}
func (q *JobQueue) ensureIndex() {
	if q.index == nil || len(q.index.byID) != len(q.Jobs) {
		q.Reindex()
	}
}
func (q *JobQueue) Get(id string) (MediaJob, bool) {
	q.ensureIndex()
	e := q.index.byID[id]
	if e == nil {
		return MediaJob{}, false
	}
	return e.job, true
}
func (q *JobQueue) Replace(job MediaJob) {
	q.ensureIndex()
	e := q.index.byID[job.ID]
	if e == nil {
		e = &jobEntry{pos: len(q.Jobs), heapIndex: -1}
		q.Jobs = append(q.Jobs, job)
	} else {
		q.index.counts[e.job.Status]--
		if e.queue != nil {
			heap.Remove(e.queue, e.heapIndex)
		}
	}
	e.job = job
	q.Jobs[e.pos] = job
	q.index.add(e)
	q.index.dirty[job.ID] = true
}
func (i *jobIndex) add(e *jobEntry) {
	i.byID[e.job.ID] = e
	i.counts[e.job.Status]++
	e.queue = nil
	e.heapIndex = -1
	switch e.job.Status {
	case JobPending:
		if e.job.Attempts >= MaxJobAttempts {
			return
		}
		e.queue = &i.ready
		if !e.job.NextAttemptAt.IsZero() {
			e.queue = &i.delayed
		}
	case JobLeased:
		e.queue = &i.leased
	}
	if e.queue != nil {
		heap.Push(e.queue, e)
	}
}
func (q *JobQueue) makeDue(now time.Time) {
	for len(q.index.leased.items) > 0 {
		job := q.index.leased.items[0].job
		if job.LeaseUntil.After(now) {
			break
		}
		q.Release(job.ID, job.LeaseOwner)
	}
	for len(q.index.delayed.items) > 0 {
		e := q.index.delayed.items[0]
		if e.job.NextAttemptAt.After(now) {
			break
		}
		heap.Pop(&q.index.delayed)
		e.queue = &q.index.ready
		heap.Push(e.queue, e)
	}
}
func (q *JobQueue) Changes() []MediaJob {
	q.ensureIndex()
	changes := make([]MediaJob, 0, len(q.index.dirty))
	for id := range q.index.dirty {
		changes = append(changes, q.index.byID[id].job)
	}
	return changes
}
func (q *JobQueue) Saved() { q.ensureIndex(); clear(q.index.dirty) }
func (q *JobQueue) SetProgress(id, owner string, progress int) {
	q.ensureIndex()
	e := q.index.byID[id]
	if e != nil && e.job.Status == JobLeased && e.job.LeaseOwner == owner && progress > e.job.Progress && progress < 100 {
		e.job.Progress = progress
		q.Jobs[e.pos].Progress = progress
	}
}
func (q *JobQueue) Progress() (working, progress int) {
	q.ensureIndex()
	for _, e := range q.index.leased.items {
		if e.job.Progress < 100 {
			working++
			progress += e.job.Progress
		}
	}
	if working > 0 {
		progress /= working
	}
	return
}
