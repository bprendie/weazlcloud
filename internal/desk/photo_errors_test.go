package desk

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func TestPhotoAPIReportsWorkerAndLocalSizeLimits(t *testing.T) {
	for _, err := range []error{library.ErrPreviewTooLarge, previewrpc.ErrTooLarge} {
		out := httptest.NewRecorder()
		photoAPIError(out, err)
		if out.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%v: status %d", err, out.Code)
		}
	}
}
