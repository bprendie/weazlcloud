package mobileparts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestTenMiBDuplicateConsumesAndVerifiesEntireStream(t *testing.T) {
	m, res, _ := fixture(t)
	data := bytes.Repeat([]byte("p"), 10<<20)
	create(t, m, res, data)
	first := appendPart(t, m, res, data, 0)
	before := snapshotSession(t, keyFor(res, sessionID))
	body := &countingBody{reader: bytes.NewReader(data)}
	duplicate, err := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), body)
	if err != nil || body.read != int64(len(data)) || !body.eof {
		t.Fatalf("duplicate did not drain request: read=%d eof=%v err=%v", body.read, body.eof, err)
	}
	if duplicate.Components[0] != first.Components[0] || !duplicate.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("duplicate refreshed counters or inactivity")
	}
	unchangedSessionFiles(t, before, snapshotSession(t, keyFor(res, sessionID)))
	wrong := bytes.Repeat([]byte("x"), len(data))
	if _, err = m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(wrong)); !errors.Is(err, ErrChecksum) {
		t.Fatalf("trusted duplicate hash header over actual bytes: %v", err)
	}
	for _, body := range [][]byte{data[:len(data)-1], append(append([]byte(nil), data...), 1)} {
		if _, err = m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(body)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("duplicate length: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.Append(ctx, res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(data)); !errors.Is(err, context.Canceled) {
		t.Fatalf("duplicate cancellation: %v", err)
	}
}

type countingBody struct {
	reader io.Reader
	read   int64
	eof    bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += int64(n)
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}
