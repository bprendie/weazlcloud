package restic

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func readerFrame(kind byte, id, size uint64, body []byte) []byte {
	raw := make([]byte, 25+len(body))
	copy(raw, "WRD2")
	raw[4] = kind
	binary.BigEndian.PutUint64(raw[5:13], id)
	binary.BigEndian.PutUint64(raw[13:21], size)
	binary.BigEndian.PutUint32(raw[21:25], uint32(len(body)))
	copy(raw[25:], body)
	return raw
}

func TestReaderStreamRejectsMalformedAndTruncatedFrames(t *testing.T) {
	for _, mode := range []string{"oversized", "truncated", "wrong_id", "duplicate_begin", "incomplete", "duplicate_complete"} {
		t.Run(mode, func(t *testing.T) {
			ch := make(chan ReaderResult, 2)
			r := &Reader{next: 1, ready: make(chan struct{}), pending: map[uint64]chan ReaderResult{1: ch}, streams: map[uint64]*readerStream{1: {limit: 3, result: ReaderResult{ID: 1}}}}
			raw := readerFrame(1, 0, 0, nil)
			raw = append(raw, readerFrame(2, 1, 3, nil)...)
			switch mode {
			case "oversized":
				raw = append(raw, readerFrame(3, 1, 0, []byte("four"))...)
			case "truncated":
				raw = append(raw, readerFrame(3, 1, 0, []byte("abc"))[:26]...)
			case "wrong_id":
				raw = append(raw, readerFrame(3, 2, 0, []byte("abc"))...)
			case "duplicate_begin":
				raw = append(raw, readerFrame(2, 1, 3, nil)...)
			case "incomplete":
				raw = append(raw, readerFrame(4, 1, 0, nil)...)
			case "duplicate_complete":
				raw = append(raw, readerFrame(3, 1, 0, []byte("abc"))...)
				raw = append(raw, readerFrame(4, 1, 0, nil)...)
			}
			raw = append(raw, readerFrame(4, 1, 0, nil)...)
			r.receiveStream(bytes.NewReader(raw))
			want := 0
			if mode == "duplicate_complete" {
				want = 1
			}
			if len(ch) != want {
				t.Fatalf("accepted invalid result: %d", len(ch))
			}
		})
	}
}

func TestReaderStreamCancellationDrainsWithoutRetainingBytes(t *testing.T) {
	r := &Reader{next: 1, ready: make(chan struct{}), pending: map[uint64]chan ReaderResult{}, streams: map[uint64]*readerStream{1: {limit: 3, canceled: true}}}
	if !r.acceptFrame(2, 1, 10, nil) || !r.acceptFrame(3, 1, 0, []byte("abc")) {
		t.Fatal("canceled frame rejected")
	}
	if len(r.streams[1].result.Body) != 0 {
		t.Fatal("canceled image retained")
	}
	if !r.acceptFrame(4, 1, 0, nil) || len(r.streams) != 0 {
		t.Fatal("canceled request not drained")
	}
	if r.acceptFrame(4, 1, 0, nil) {
		t.Fatal("duplicate terminal accepted")
	}
}
