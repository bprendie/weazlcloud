package desk

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func receiveMobile[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for finalizer")
		var zero T
		return zero
	}
}

func TestMobilePoolLimitsDuplicatesAndRefill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newMobileFinalizerPool(ctx, mobileFinalizeLimits{3, 2})
	defer func() { cancel(); p.close() }()
	started := make(chan mobileFinalizeKey, 20)
	release := make(map[mobileFinalizeKey]chan struct{})
	for _, owner := range []string{"a", "b", "c"} {
		for i := 0; i < 4; i++ {
			release[mobileFinalizeKey{owner, fmt.Sprint(i)}] = make(chan struct{})
		}
	}
	queues := func(owners ...string) []mobileOwnerJobs {
		var out []mobileOwnerJobs
		for _, owner := range owners {
			q := mobileOwnerJobs{owner: owner}
			for i := 0; i < 4; i++ {
				key := mobileFinalizeKey{owner, fmt.Sprint(i)}
				q.jobs = append(q.jobs, mobileFinalizeJob{key, func(ctx context.Context) {
					started <- key
					select {
					case <-ctx.Done():
					case <-release[key]:
					}
				}})
			}
			out = append(out, q)
		}
		return out
	}
	p.schedule(queues("a"))
	for range 2 {
		if k := receiveMobile(t, started); k.owner != "a" {
			t.Fatal(k)
		}
	}
	if len(p.active) != 2 {
		t.Fatalf("per-owner limit: %v", p.active)
	}
	// Repeated polls must skip both active IDs, even with a spare global slot.
	for range 10 {
		p.schedule(queues("a"))
	}
	if len(p.active) != 2 {
		t.Fatalf("duplicate or excess owner admission: %v", p.active)
	}
	p.schedule(queues("a", "b", "c"))
	b := receiveMobile(t, started)
	if b.owner != "b" || len(p.active) != 3 {
		t.Fatalf("global/fair admission: %v %v", b, p.active)
	}
	// A completed fast job frees capacity while both slow owner-a jobs remain.
	close(release[b])
	p.complete(receiveMobile(t, p.done))
	p.schedule(queues("a", "b", "c"))
	c := receiveMobile(t, started)
	if c.owner != "c" {
		t.Fatalf("last served owner starved next owner: %v", c)
	}
	if len(p.active) != 3 || p.owners["a"] != 2 {
		t.Fatalf("refill limits: %v", p.active)
	}
	select {
	case unexpected := <-started:
		t.Fatalf("duplicate/excess execution: %v", unexpected)
	default:
	}
}

func TestMobilePoolRoundRobinAndOwnerScopedIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newMobileFinalizerPool(ctx, mobileFinalizeLimits{2, 2})
	defer func() { cancel(); p.close() }()
	started := make(chan mobileFinalizeKey, 2)
	queues := func() []mobileOwnerJobs {
		var out []mobileOwnerJobs
		for _, owner := range []string{"a", "b", "c"} {
			q := mobileOwnerJobs{owner: owner}
			for _, id := range []string{"same", "second"} {
				key := mobileFinalizeKey{owner, id}
				q.jobs = append(q.jobs, mobileFinalizeJob{key, func(ctx context.Context) { started <- key; <-ctx.Done() }})
			}
			out = append(out, q)
		}
		return out
	}
	p.schedule(queues())
	got := map[mobileFinalizeKey]bool{}
	for range 2 {
		got[receiveMobile(t, started)] = true
	}
	if !got[mobileFinalizeKey{"a", "same"}] || !got[mobileFinalizeKey{"b", "same"}] {
		t.Fatalf("first round must serve distinct owners, even with identical IDs: %v", got)
	}
}

func TestMobilePoolCancellationDrainsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newMobileFinalizerPool(ctx, mobileFinalizeLimits{2, 2})
	started := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	q := mobileOwnerJobs{owner: "a"}
	for i := range 2 {
		q.jobs = append(q.jobs, mobileFinalizeJob{mobileFinalizeKey{"a", fmt.Sprint(i)}, func(ctx context.Context) {
			started <- struct{}{}
			<-ctx.Done()
			cancelled <- struct{}{}
			<-release
		}})
	}
	p.schedule([]mobileOwnerJobs{q})
	for range 2 {
		receiveMobile(t, started)
	}
	cancel()
	for range 2 {
		receiveMobile(t, cancelled)
	}
	closed := make(chan struct{})
	go func() { p.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("shutdown did not wait for worker cleanup")
	default:
	}
	release <- struct{}{}
	release <- struct{}{}
	receiveMobile(t, closed)
	// Cancellation prevents any subsequent admission (even if capacity exists).
	p.complete(receiveMobile(t, p.done))
	p.complete(receiveMobile(t, p.done))
	p.schedule([]mobileOwnerJobs{{owner: "a", jobs: []mobileFinalizeJob{
		{key: mobileFinalizeKey{"a", "new"}, run: func(context.Context) { t.Error("started after cancellation") }},
	}}})

	if len(p.active) != 0 {
		t.Fatal("admitted work after cancellation")
	}
}

func TestMobilePoolSkipsActiveIDWithSpareOwnerCapacity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newMobileFinalizerPool(ctx, mobileFinalizeLimits{4, 4})
	defer func() { cancel(); p.close() }()
	started := make(chan string, 10)
	queues := func() []mobileOwnerJobs {
		return []mobileOwnerJobs{{owner: "a", jobs: []mobileFinalizeJob{
			{key: mobileFinalizeKey{"a", "same"}, run: func(ctx context.Context) { started <- "same"; <-ctx.Done() }},
		}}}
	}
	p.schedule(queues())
	receiveMobile(t, started)
	for range 10 {
		p.schedule(queues())
	}
	if len(p.active) != 1 || p.owners["a"] != 1 {
		t.Fatalf("duplicate admitted across ticks: %v %v", p.active, p.owners)
	}
	// A distinct session still starts immediately despite the blocked original.
	p.schedule([]mobileOwnerJobs{{owner: "a", jobs: []mobileFinalizeJob{
		{key: mobileFinalizeKey{"a", "next"}, run: func(ctx context.Context) { started <- "next"; <-ctx.Done() }},
	}}})
	if got := receiveMobile(t, started); got != "next" {
		t.Fatalf("duplicate execution: %s", got)
	}
}
