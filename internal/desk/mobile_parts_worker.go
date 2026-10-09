package desk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"time"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func mobileVaultContext(parent context.Context, res *filesvc.Resource) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(res.Vault.Session(), cancel)
	return ctx, func() { stop(); cancel() }
}

// The pool persists across polling rounds; only shutdown waits for workers.
func (h *Handler) runMobileParts(ctx context.Context) {
	if h.mobileParts == nil {
		return
	}
	pool := newMobileFinalizerPool(ctx, h.mobileFinalizerLimits())
	log.Printf("mobile finalize workers=%d per_owner=%d", pool.limits.global, pool.limits.perOwner)
	defer pool.close()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	nextSweep := time.Now()
	nextGrantReport := time.Time{}
	var queues []mobileOwnerJobs
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-pool.done:
			pool.complete(key)
			// Refill from the bounded scan immediately; a fast batch must not
			// idle until the next one-second discovery tick.
			pool.schedule(queues)
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			owners := h.users.Users()
			denied := 0
			queues = nil
			sweep := !time.Now().Before(nextSweep)
			if sweep {
				nextSweep = time.Now().Add(15 * time.Minute)
			}
			for _, user := range owners {
				if ctx.Err() != nil {
					return
				}
				if user.Disabled || user.Deleting {
					continue
				}
				res := h.registry.For(user)
				if !res.Vault.Unlocked() {
					continue
				}
				lease, release, ok := h.registry.Enter(ctx, user.ID)
				if !ok {
					continue
				}
				pending, err := h.mobileParts.Pending(res)
				if err != nil {
					log.Printf("mobile queue warning: owner=%s error=%v", user.ID, err)
				}
				queue := mobileOwnerJobs{owner: user.ID}
				for _, session := range pending {
					if pool.active[mobileFinalizeKey{user.ID, session.ID}] {
						continue
					}
					if h.authorizeMobilePart(session) != nil {
						denied++
						continue
					}
					queue.jobs = append(queue.jobs, mobileFinalizeJob{
						key: mobileFinalizeKey{user.ID, session.ID},
						run: func(ctx context.Context) { h.processMobilePart(ctx, user, res, session.ID) },
					})
				}
				queues = append(queues, queue)
				// Sweep only unlocked owners under their lifecycle lease.
				if sweep && lease.Err() == nil {
					_ = h.mobileParts.Sweep(res)
				}
				release()
			}
			if denied > 0 && !time.Now().Before(nextGrantReport) {
				log.Printf("mobile queue authorization deferred=%d; existing upload admission must be renewed", denied)
				nextGrantReport = time.Now().Add(time.Minute)
			}
			pool.schedule(queues)
		}
	}
}

func (h *Handler) processMobilePart(ctx context.Context, user users.User, res *filesvc.Resource, id string) {
	lease, release, ok := h.registry.Enter(ctx, user.ID)
	if !ok {
		return
	}
	defer release()
	job, cancel := mobileVaultContext(lease, res)
	defer cancel()
	stopGrant := h.watchMobileGrant(job, cancel, res, id)
	defer stopGrant()
	err := h.mobileParts.Process(job, res, id, h.authorizeMobilePart, func(s mobileparts.Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
		result, err := h.commitMobilePart(job, res, user, s, open)
		return result, mobileCommitFailure(err)
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, vault.ErrLocked) {
		log.Printf("mobile finalize deferred: owner=%s upload=%s error=%v", user.ID, id, err)
	}
}
func (h *Handler) authorizeMobilePart(s mobileparts.Session) error {
	var intent mobileIntent
	if json.Unmarshal(s.Spec.Payload, &intent) != nil || intent.Grant.OwnerID != s.OwnerID || intent.Grant.DeviceID != s.Spec.DeviceID {
		return mobileparts.ErrCorrupt
	}
	return h.users.CheckDeviceGrant(intent.Grant, partScopes(s.Spec.Kind)...)
}
func (h *Handler) commitMobilePart(ctx context.Context, res *filesvc.Resource, user users.User, s mobileparts.Session, open func(string) (io.ReadCloser, error)) (json.RawMessage, error) {
	if s.RetryAttempts > 0 {
		// Isolate retried members so one broken source cannot repeatedly abort
		// the healthy members of its original batch.
		ctx = library.WithPhotoStorageSingle(ctx)
	}
	var intent mobileIntent
	if e := json.Unmarshal(s.Spec.Payload, &intent); e != nil {
		return nil, mobileparts.ErrCorrupt
	}
	guard := func(publish func() error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if !res.Vault.Unlocked() {
			return vault.ErrLocked
		}
		return h.users.WithDeviceGrant(intent.Grant, publish, partScopes(s.Spec.Kind)...)
	}
	if s.Spec.Kind == "photo" {
		out, e := h.photoUploads.FinalizePartsGuarded(ctx, res, user, s.ID, s.Spec.DeviceID, open, guard)
		if e != nil {
			return nil, e
		}
		return json.Marshal(out)
	}
	m := backup.New(h.uploads)
	m.SetCommitGuard(guard)
	body, e := open("original")
	if e != nil {
		return nil, e
	}
	defer body.Close()
	out, e := m.FinalizeParts(ctx, res, user, s.ID, s.Spec.DeviceID, body)
	if e != nil {
		return nil, e
	}
	return json.Marshal(out)
}

// Bound cancellation latency during long storage/verification work. The final
// atomic grant guard remains the publication barrier, even between ticks.
func (h *Handler) watchMobileGrant(ctx context.Context, cancel context.CancelFunc, res *filesvc.Resource, id string) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	s, e := h.mobileParts.Session(res, id)
	if e != nil {
		cancel()
		return func() {}
	}
	go func() {
		defer close(stopped)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-tick.C:
				if h.authorizeMobilePart(s) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done); <-stopped }
}
