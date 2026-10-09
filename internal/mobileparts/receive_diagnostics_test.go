package mobileparts

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/users"
	"io"
	"syscall"
	"testing"
)

func TestReceiveFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{{nil, "ok"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "deadline"}, {io.ErrUnexpectedEOF, "unexpected_eof"}, {ErrChecksum, "checksum"}, {users.ErrNoSession, "authorization"}, {syscall.ENOSPC, "capacity"}, {ErrBusy, "admission_busy"}, {errors.New("disk failure"), "storage"}} {
		if got := ReceiveFailureClass(tc.err); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
