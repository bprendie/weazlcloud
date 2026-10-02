package previewrpc

import (
	"bytes"
	"errors"
	"testing"
)

func TestTypedBundleFailurePreservesEnvironmentAndLegacyRejection(t *testing.T) {
	for _, expected := range []error{ErrEnvironment, ErrUnavailable, ErrTimeout, ErrTooLarge, ErrRejected} {
		var b bytes.Buffer
		if err := EndBundleError(&b, expected); err != nil {
			t.Fatal(err)
		}
		if err := ReadBundle(&b, []int{320}, func(Variant) error { return nil }); !errors.Is(err, expected) {
			t.Fatal(expected, err)
		}
	}
	var b bytes.Buffer
	_ = EndBundle(&b, true)
	if err := ReadBundle(&b, []int{320}, func(Variant) error { return nil }); !errors.Is(err, ErrRejected) {
		t.Fatal(err)
	}
}
