package app

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/idle"
	"github.com/bprendie/weazlcloud/internal/ready"
)

func trackRequests(next http.Handler, activity *idle.Coordinator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if activity == nil || !idle.TracksRequest(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		release := activity.Track()
		defer release()
		next.ServeHTTP(w, r)
	})
}

func health(next http.Handler, dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live" && r.Method == http.MethodGet {
			ready.Live(w, r)
			return
		}
		if r.URL.Path == "/ready" && r.Method == http.MethodGet {
			ready.Storage(w, r, func() error {
				return storageReady(dataDir)
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
