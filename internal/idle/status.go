package idle

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type JobStatus struct {
	Name           string     `json:"name"`
	LastAttempt    *time.Time `json:"last_attempt,omitempty"`
	LastSuccess    *time.Time `json:"last_success,omitempty"`
	LastDurationMS int64      `json:"last_duration_ms,omitempty"`
	ReclaimedBytes int64      `json:"reclaimed_bytes,omitempty"`
	LastResult     string     `json:"last_result"`
	ErrorCategory  string     `json:"error_category,omitempty"`
}

func (c *Coordinator) SetStatusPath(path string) error {
	c.mu.Lock()
	c.statusPath = path
	c.mu.Unlock()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("maintenance status could not be read")
	}
	var list []JobStatus
	if err := json.Unmarshal(b, &list); err != nil {
		return errors.New("maintenance status is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, status := range list {
		if status.Name != "" {
			c.history[status.Name] = status
		}
	}
	for i := range c.jobs {
		c.jobs[i].status = c.history[c.jobs[i].name]
	}
	return nil
}

func (c *Coordinator) Status() []JobStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]JobStatus, len(c.jobs))
	for i, job := range c.jobs {
		out[i] = job.status
		out[i].Name = job.name
		if out[i].LastResult == "" {
			out[i].LastResult = "not_run"
		}
	}
	return out
}

func (c *Coordinator) updateStatus(index int, update func(*JobStatus)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index >= len(c.jobs) {
		return
	}
	status := &c.jobs[index].status
	status.Name = c.jobs[index].name
	update(status)
	c.history[status.Name] = *status
}

func (c *Coordinator) persistStatus() {
	c.mu.Lock()
	path := c.statusPath
	list := make([]JobStatus, len(c.jobs))
	for i, j := range c.jobs {
		list[i] = j.status
	}
	c.mu.Unlock()
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		err = cryptox.AtomicWrite(path, append(b, '\n'), 0o600)
	}
	if err != nil {
		log.Printf("maintenance status persistence category=%s", ErrorCategory(err))
	}
}

func ErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return "permission"
	}
	if errors.Is(err, syscall.ENOSPC) {
		return "no_space"
	}
	if errors.Is(err, syscall.EROFS) {
		return "read_only"
	}
	if errors.Is(err, syscall.EIO) {
		return "storage_io"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "missing_data"
	}
	return "operation_failed"
}
