package desk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func mobileVaultContext(parent context.Context, res *filesvc.Resource) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(res.Vault.Session(), cancel)
	return ctx, func() { stop(); cancel() }
}

// At most two finalizers globally, one per owner in each fair round. Staging parts are
// independent of finalization; uploads continue while older originals commit.
func (h *Handler) runMobileParts(ctx context.Context) {
	if h.mobileParts == nil {
		return
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	offset := 0
	nextSweep := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		owners := h.users.Users()
		if len(owners) == 0 {
			continue
		}
		var wg sync.WaitGroup
		slots := make(chan struct{}, mobileFinalizerLimit())
		for i := range owners {
			user := owners[(offset+i)%len(owners)]
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
			pending, e := h.mobileParts.Pending(res)
			if e != nil {
				log.Printf("mobile queue warning: owner=%s error=%v", user.ID, e)
			}
			if len(pending) == 0 {
				release()
				continue
			}
			var chosen *mobileparts.Session
			for j := range pending {
				if h.authorizeMobilePart(pending[j]) == nil {
					chosen = &pending[j]
					break
				}
			}
			if chosen == nil {
				release()
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				release()
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(user users.User, res *filesvc.Resource, id string, lease context.Context, release func()) {
				defer wg.Done()
				defer func() { <-slots }()
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
			}(user, res, chosen.ID, lease, release)
		}
		wg.Wait()
		offset = (offset + 1) % len(owners)
		if time.Now().Before(nextSweep) {
			continue
		}
		nextSweep = time.Now().Add(15 * time.Minute)
		// Sweep only unlocked owners; private state never uses an admin key.
		for _, user := range owners {
			if !user.Disabled && !user.Deleting {
				res := h.registry.For(user)
				if res.Vault.Unlocked() {
					lease, release, ok := h.registry.Enter(ctx, user.ID)
					if ok {
						if lease.Err() == nil {
							_ = h.mobileParts.Sweep(res)
						}
						release()
					}
				}
			}
		}
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

func mobileFinalizerLimit() int { return min(2, max(1, runtime.GOMAXPROCS(0)/2)) }
