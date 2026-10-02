package mobileparts

import "errors"

// Commit errors may expose FailureCode() string to preserve an actionable
// application failure through wrappers. Checksums and cancellation take priority.
func failureCode(err error) string {
	var coded interface{ FailureCode() string }
	if errors.As(err, &coded) {
		if code := coded.FailureCode(); code != "" {
			return code
		}
	}
	return "commit_failed"
}
