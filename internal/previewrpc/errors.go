package previewrpc

import (
	"context"
	"errors"
	"net/http"
)

var ErrEnvironment = errors.New("photo worker environment unavailable")
var ErrTimeout = errors.New("photo worker timed out")

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrEnvironment):
		return "worker_environment"
	case errors.Is(err, ErrUnavailable):
		return "worker_unavailable"
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return "worker_timeout"
	case errors.Is(err, ErrTooLarge):
		return "unsupported_size"
	default:
		return "invalid_or_unsupported_media"
	}
}
func ErrorForCode(code string) error {
	switch code {
	case "worker_environment":
		return ErrEnvironment
	case "worker_unavailable":
		return ErrUnavailable
	case "worker_timeout":
		return ErrTimeout
	case "unsupported_size":
		return ErrTooLarge
	default:
		return ErrRejected
	}
}
func ResponseError(res *http.Response) error {
	code := res.Header.Get("X-Weazl-Preview-Error")
	if code != "" {
		return ErrorForCode(code)
	}
	if res.StatusCode >= 500 {
		return ErrUnavailable
	}
	if res.StatusCode == http.StatusRequestEntityTooLarge {
		return ErrTooLarge
	}
	return ErrRejected
}
