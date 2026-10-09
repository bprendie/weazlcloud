package desk

import (
	"context"
	"sync"
)

type mobileFinalizeKey struct{ owner, id string }
type mobileFinalizeJob struct {
	key mobileFinalizeKey
	run func(context.Context)
}
type mobileOwnerJobs struct {
	owner string
	jobs  []mobileFinalizeJob
}

// All bookkeeping belongs to the polling goroutine. Workers only execute jobs
// and report completion. There are at most global admitted jobs, including any
// waiting for a worker; durable pending sessions remain in the staging store.
type mobileFinalizerPool struct {
	ctx       context.Context
	limits    mobileFinalizeLimits
	jobs      chan mobileFinalizeJob
	done      chan mobileFinalizeKey
	active    map[mobileFinalizeKey]bool
	owners    map[string]int
	lastOwner string
	wg        sync.WaitGroup
}

func newMobileFinalizerPool(ctx context.Context, limits mobileFinalizeLimits) *mobileFinalizerPool {
	p := &mobileFinalizerPool{ctx: ctx, limits: limits,
		jobs: make(chan mobileFinalizeJob, limits.global), done: make(chan mobileFinalizeKey, limits.global),
		active: make(map[mobileFinalizeKey]bool), owners: make(map[string]int)}
	for range limits.global {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				if ctx.Err() == nil {
					job.run(ctx)
				}
				p.done <- job.key
			}
		}()
	}
	return p
}

func (p *mobileFinalizerPool) close() { close(p.jobs); p.wg.Wait() }

func (p *mobileFinalizerPool) complete(key mobileFinalizeKey) {
	if p.active[key] {
		delete(p.active, key)
		p.owners[key.owner]--
		if p.owners[key.owner] == 0 {
			delete(p.owners, key.owner)
		}
	}
}

// Take one session per owner per round, starting after the last owner served.
// Retaining that cursor prevents a busy first owner from starving later owners
// when only one slot becomes free. Active IDs survive every polling round.
func (p *mobileFinalizerPool) schedule(queues []mobileOwnerJobs) {
	start := 0
	for i, q := range queues {
		if q.owner == p.lastOwner {
			start = (i + 1) % len(queues)
			break
		}
	}
	for len(p.active) < p.limits.global && p.ctx.Err() == nil {
		progress := false
		for i := range queues {
			q := &queues[(start+i)%len(queues)]
			if len(p.active) == p.limits.global || p.ctx.Err() != nil {
				return
			}
			if p.owners[q.owner] >= p.limits.perOwner {
				continue
			}
			for len(q.jobs) > 0 {
				job := q.jobs[0]
				q.jobs = q.jobs[1:]
				if p.active[job.key] {
					continue
				}
				p.active[job.key] = true
				p.owners[q.owner]++
				p.lastOwner = q.owner
				p.jobs <- job
				progress = true
				break
			}
		}
		if !progress {
			return
		}
	}
}
