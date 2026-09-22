package idle

import (
	"context"
	"sync"
	"time"
)

const DefaultQuietPeriod = 5 * time.Minute
const PollInterval = time.Second

type Job func(context.Context) error

type Coordinator struct {
	mu           sync.Mutex
	quiet        time.Duration
	now          func() time.Time
	lastActivity time.Time
	active       int
	running      bool
	stopJob      context.CancelFunc
	jobDone      chan struct{}
	jobs         []Job
}

func New(quiet time.Duration, now func() time.Time) *Coordinator {
	if quiet <= 0 {
		quiet = DefaultQuietPeriod
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{quiet: quiet, now: now, lastActivity: now()}
}

func (c *Coordinator) Register(job Job) {
	if job == nil {
		return
	}
	c.mu.Lock()
	c.jobs = append(c.jobs, job)
	c.mu.Unlock()
}

func (c *Coordinator) Track() func() {
	c.mu.Lock()
	c.active++
	c.lastActivity = c.now()
	var wait <-chan struct{}
	if c.stopJob != nil {
		c.stopJob()
		wait = c.jobDone
	}
	c.mu.Unlock()
	if wait != nil {
		<-wait
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.active--
			if c.active == 0 {
				c.lastActivity = c.now()
			}
			c.mu.Unlock()
		})
	}
}

func (c *Coordinator) RunOnce(parent context.Context) bool {
	if parent.Err() != nil {
		return false
	}
	c.mu.Lock()
	if c.active != 0 || c.running || len(c.jobs) == 0 || c.now().Sub(c.lastActivity) < c.quiet {
		c.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	c.stopJob = cancel
	c.jobDone = done
	c.running = true
	jobs := append([]Job(nil), c.jobs...)
	c.mu.Unlock()

	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		_ = job(ctx)
	}
	c.mu.Lock()
	c.running = false
	c.stopJob = nil
	c.jobDone = nil
	close(done)
	if c.active == 0 {
		c.lastActivity = c.now()
	}
	c.mu.Unlock()
	cancel()
	return true
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			if c.stopJob != nil {
				c.stopJob()
			}
			c.mu.Unlock()
			return
		case <-ticker.C:
			c.RunOnce(ctx)
		}
	}
}

func (c *Coordinator) Active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func TracksRequest(path string) bool {
	switch path {
	case "/live", "/ready", "/api/library/events":
		return false
	default:
		return true
	}
}
