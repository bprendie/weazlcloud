package users

import (
	"testing"
	"time"
)

func TestSlowPublicationDoesNotBlockGrantReads(t *testing.T) {
	s, u, _ := deviceFixture(t)
	_, token, e := s.CreateScopedDevice(u.ID, "phone", []string{PhotosWrite})
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.GrantForRequest(deviceRequest("POST", "/api/v1/photos/uploads", token), PhotosWrite)
	if e != nil {
		t.Fatal(e)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.WithDeviceGrant(g, func() error { close(entered); <-release; return nil }, PhotosWrite)
	}()
	<-entered
	defer func() {
		close(release)
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	reads := make(chan error, 1)
	go func() {
		for range 100 {
			if e := s.CheckDeviceGrant(g, PhotosWrite); e != nil {
				reads <- e
				return
			}
		}
		_, e := s.DeviceForRequest(deviceRequest("GET", "/api/v1/photos/uploads/id", token))
		reads <- e
	}()
	select {
	case e := <-reads:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("grant reads waited for slow publication")
	}
}
