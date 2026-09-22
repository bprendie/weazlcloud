package app

import (
	"log"
	"net/http"
	"strings"
	"time"
)

type observedWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *observedWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *observedWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func observed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ow := &observedWriter{ResponseWriter: w}
		next.ServeHTTP(ow, r)
		status := ow.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http method=%s path=%s status=%d duration_ms=%d", r.Method, safePath(r.URL.Path), status, time.Since(started).Milliseconds())
	})
}

func safePath(path string) string {
	if strings.HasPrefix(path, "/g/") {
		return "/g/:capsule"
	}
	return path
}
