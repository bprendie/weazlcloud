package main

import (
	"errors"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"net/http"
)

func workerError(w http.ResponseWriter, err error) {
	code := previewrpc.ErrorCode(err)
	status := http.StatusUnprocessableEntity
	if errors.Is(err, previewrpc.ErrEnvironment) || errors.Is(err, previewrpc.ErrUnavailable) || code == "worker_timeout" {
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "30")
	}
	w.Header().Set("X-Weazl-Preview-Error", code)
	http.Error(w, code, status)
}
