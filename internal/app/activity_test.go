package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/idle"
)

func TestStorageActivityMiddlewareExcludesHealthAndSSE(t *testing.T) {
	coordinator := idle.New(time.Minute, time.Now)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dav/" && coordinator.Active() != 1 {
			t.Errorf("DAV operation active count=%d", coordinator.Active())
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := trackRequests(next, coordinator)
	for _, path := range []string{"/live", "/ready", "/api/library/events"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if coordinator.Active() != 0 {
			t.Fatalf("%s counted as storage activity", path)
		}
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PROPFIND", "/dav/", nil))
	if coordinator.Active() != 0 {
		t.Fatalf("storage request activity leaked: %d", coordinator.Active())
	}
}
