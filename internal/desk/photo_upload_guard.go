package desk

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/users"
	"net/http"
	"time"
)

// Cookie compatibility is unchanged. A bearer transfer binds to the admitted
// vault session and grant; cancellation saves work, final CAS enforces authority.
func (h *Handler) photoUploadCommitGuard(r *http.Request, res *filesvc.Resource) (context.Context, func(func() error) error, func(), error) {
	if r.Header.Get("Authorization") == "" {
		return r.Context(), nil, func() {}, nil
	}
	grant, err := h.users.GrantForRequest(r, users.PhotosWrite)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := mobileVaultContext(r.Context(), res)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if h.users.CheckDeviceGrant(grant, users.PhotosWrite) != nil {
					cancel()
					return
				}
			}
		}
	}()
	guard := func(publish func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return h.users.WithDeviceGrant(grant, publish, users.PhotosWrite)
	}
	return ctx, guard, cancel, nil
}
