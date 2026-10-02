package filesvc

import "context"

// StopAdmission also cancels event streams and private preview leases so they
// cannot keep HTTP shutdown waiting indefinitely. It never deletes owner data.
func (r *Registry) StopAdmission() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closing = true
	for _, gate := range r.gates {
		gate.blocked = true
		for _, cancel := range gate.cancels {
			cancel()
		}
	}
}

// Shutdown runs after HTTP handlers and maintenance have stopped. Export private
// job state before locking vaults; retain ready archives and resumable jobs.
func (r *Registry) Shutdown(ctx context.Context) error {
	r.StopAdmission()
	r.mu.Lock()
	resources := make(map[string]*Resource, len(r.items))
	for id, resource := range r.items {
		resources[id] = resource
	}
	r.mu.Unlock()
	for id, resource := range resources {
		if err := r.Block(ctx, id); err != nil {
			return err
		}
		if err := resource.Archives.Checkpoint(ctx); err != nil {
			return err
		}
		if err := resource.Lib.Drain(ctx); err != nil {
			return err
		}
		resource.LockVault()
		resource.Changes.Close()
	}
	return nil
}

// Checkpoint stops workers while preserving their encrypted restart records.
// Drain's destructive cleanup is reserved for account removal/explicit locking.
func (m *ArchiveManager) Checkpoint(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	for _, job := range m.jobs {
		job.cancel()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
