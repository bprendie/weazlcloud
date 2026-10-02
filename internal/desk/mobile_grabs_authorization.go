package desk

import (
	"context"
	"net/http"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Keep grant checks out of vault/library/capsule storage callbacks. The final
// guard takes users.mu only around a pre-serialized metadata atomic write.
func (h *Handler) mobileGrabAuthorization(r *http.Request, v *vault.Vault) (*http.Request, capsule.MobilePublicationGuard, func(), error) {
	ctx, cancel := context.WithCancel(r.Context())
	stopVault := context.AfterFunc(v.Session(), cancel)
	cleanup := func() { stopVault(); cancel() }
	var grant users.DeviceGrant
	native := h.users != nil && r.Header.Get("Authorization") != ""
	scopes := []string{users.GrabsWrite, users.FilesRead}
	if r.URL.Path == "/api/v1/photos/grabs" {
		scopes[1] = users.PhotosRead
	}
	if native {
		var err error
		grant, err = h.users.GrantForRequest(r, scopes...)
		if err != nil {
			cleanup()
			return r, nil, nil, err
		}
		stop := watchMobileGrabGrant(ctx, cancel, func() error { return h.users.CheckDeviceGrant(grant, scopes...) })
		cleanup = func() { stopVault(); cancel(); stop() }
	}
	publish := func(commit func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !v.Unlocked() {
			return vault.ErrLocked
		}
		if !native {
			return commit()
		}
		return h.users.WithDeviceGrant(grant, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return commit()
		}, scopes...)
	}
	return r.WithContext(ctx), publish, cleanup, nil
}

func watchMobileGrabGrant(ctx context.Context, cancel context.CancelFunc, check func() error) func() {
	stop, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(stop); <-finished }
}
