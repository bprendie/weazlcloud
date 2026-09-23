package idle

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"
)

const DefaultQuietPeriod = 5 * time.Minute
const PollInterval = time.Second

type Job func(context.Context) error
type Task func(context.Context) (int64, error)

type registeredTask struct {
	name   string
	run    Task
	status JobStatus
}

type Coordinator struct {
	mu           sync.Mutex
	quiet        time.Duration
	now          func() time.Time
	lastActivity time.Time
	active       int
	running      bool
	stopJob      context.CancelFunc
	jobDone      chan struct{}
	jobs         []registeredTask
	statusPath   string
	history      map[string]JobStatus
}

func New(quiet time.Duration, now func() time.Time) *Coordinator {
	if quiet <= 0 {
		quiet = DefaultQuietPeriod
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{quiet: quiet, now: now, lastActivity: now(), history: make(map[string]JobStatus)}
}

func (c *Coordinator) Register(job Job) {
	if job == nil {
		return
	}
	c.mu.Lock()
	name := "maintenance-" + strconv.Itoa(len(c.jobs)+1)
	c.addLocked(name, func(ctx context.Context) (int64, error) { return 0, job(ctx) })
	c.mu.Unlock()
}

func (c *Coordinator) RegisterNamed(name string, job Job) {
	if job != nil {
		c.RegisterTask(name, func(ctx context.Context) (int64, error) { return 0, job(ctx) })
	}
}

func (c *Coordinator) RegisterTask(name string, task Task) {
	if task == nil {
		return
	}
	c.mu.Lock()
	c.addLocked(name, task)
	c.mu.Unlock()
}

func (c *Coordinator) addLocked(name string, task Task) {
	if name == "" {
		name = "maintenance"
	}
	status := c.history[name]
	for _, existing := range c.jobs {
		if existing.name == name {
			name += "-" + strconv.Itoa(len(c.jobs)+1)
			status = c.history[name]
			break
		}
	}
	c.jobs = append(c.jobs, registeredTask{name: name, run: task, status: status})
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
	jobs := append([]registeredTask(nil), c.jobs...)
	c.mu.Unlock()

	for i, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		started := c.now()
		c.updateStatus(i, func(s *JobStatus) { s.LastAttempt = &started; s.LastResult = "running"; s.ErrorCategory = "" })
		c.persistStatus()
		bytes, err := job.run(ctx)
		finished := c.now()
		result, category := "success", ""
		if err != nil {
			result, category = "failed", ErrorCategory(err)
		}
		if ctx.Err() != nil {
			result, category = "cancelled", "cancelled"
		}
		c.updateStatus(i, func(s *JobStatus) {
			s.LastDurationMS = finished.Sub(started).Milliseconds()
			s.ReclaimedBytes = bytes
			s.LastResult = result
			s.ErrorCategory = category
			if result == "success" {
				s.LastSuccess = &finished
			}
		})
		log.Printf("maintenance job=%s result=%s category=%s duration_ms=%d reclaimed_bytes=%d", job.name, result, category, finished.Sub(started).Milliseconds(), bytes)
		c.persistStatus()
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
			var done <-chan struct{}
			if c.stopJob != nil {
				c.stopJob()
				done = c.jobDone
			}
			c.mu.Unlock()
			if done != nil {
				<-done
			}
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
