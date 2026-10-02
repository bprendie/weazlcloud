package previewrpc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBundleFramesRejectMalformedAndPublishEarly(t *testing.T) {
	var good bytes.Buffer
	_ = WriteHash(&good, []byte{1, 2, 3, 4, 5})
	_ = WriteVariant(&good, Variant{Size: 320, Body: []byte("grid"), MIME: "image/png"})
	_ = WriteVariant(&good, Variant{Size: 1280, Body: []byte("viewer"), MIME: "image/jpeg"})
	_ = EndBundle(&good, false)
	n := 0
	if err := ReadBundle(bytes.NewReader(good.Bytes()), []int{320, 1280}, func(v Variant) error {
		n++
		if len(v.ThumbHash) != 5 {
			t.Error("hash absent")
		}
		return nil
	}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	huge := make([]byte, 7)
	binary.BigEndian.PutUint32(huge[3:], uint32(maxOutput+1))
	var duplicate bytes.Buffer
	_ = WriteVariant(&duplicate, Variant{Size: 320, Body: []byte("a"), MIME: "image/jpeg"})
	duplicate.Write(duplicate.Bytes())
	_ = EndBundle(&duplicate, false)
	var failure bytes.Buffer
	_ = EndBundle(&failure, true)
	for name, raw := range map[string][]byte{"truncated": good.Bytes()[:good.Len()-1], "huge": huge, "duplicate": duplicate.Bytes(), "failed": failure.Bytes(), "empty": nil} {
		t.Run(name, func(t *testing.T) {
			if err := ReadBundle(bytes.NewReader(raw), []int{320, 1280}, func(Variant) error { return nil }); err == nil {
				t.Fatal("accepted malformed bundle")
			}
		})
	}
}
