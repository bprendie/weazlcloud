package desk

import (
	"github.com/bprendie/weazlcloud/internal/users"
	"io"
	"time"
)

type mobileGrantReader struct {
	src        io.Reader
	store      *users.Store
	grant      users.DeviceGrant
	scopes     []string
	auth, read time.Duration
	interrupt  func()
}

func (r *mobileGrantReader) Read(p []byte) (int, error) {
	start := time.Now()
	err := r.store.CheckDeviceGrant(r.grant, r.scopes...)
	r.auth += time.Since(start)
	if err != nil {
		return 0, err
	}
	start = time.Now()
	n, err := r.src.Read(p)
	r.read += time.Since(start)
	start = time.Now()
	revoked := r.store.CheckDeviceGrant(r.grant, r.scopes...)
	r.auth += time.Since(start)
	if revoked != nil {
		return 0, revoked
	}
	return n, err
}
func (r *mobileGrantReader) Close() error {
	if r.interrupt != nil {
		r.interrupt()
	}
	if c, ok := r.src.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
