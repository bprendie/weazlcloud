package desk

import (
	"errors"
	"syscall"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type mobileCommitError struct {
	code  string
	cause error
}

func (e mobileCommitError) Error() string       { return e.cause.Error() }
func (e mobileCommitError) Unwrap() error       { return e.cause }
func (e mobileCommitError) FailureCode() string { return e.code }
func mobileCommitFailure(err error) error {
	if err == nil {
		return nil
	}
	code := ""
	switch {
	case errors.Is(err, catalog.ErrRevisionMismatch), errors.Is(err, catalog.ErrConflict):
		code = "stale_revision"
	case errors.Is(err, backup.ErrPaused):
		code = "source_inactive"
	case errors.Is(err, quota.ErrExceeded), errors.Is(err, syscall.ENOSPC):
		code = "insufficient_storage"
	case errors.Is(err, users.ErrNoSession), errors.Is(err, users.ErrInsufficientScope):
		code = "device_authorization_required"
	case errors.Is(err, vault.ErrLocked):
		code = "vault_locked"
	}
	if code != "" {
		return mobileCommitError{code, err}
	}
	return err
}
