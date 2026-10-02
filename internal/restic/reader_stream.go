package restic

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
)

type readerStream struct {
	limit    int
	started  bool
	canceled bool
	received uint64
	result   ReaderResult
}

// ReadImage streams bounded binary chunks into the caller-admitted destination.
// The helper never buffers a second complete source or base64 image response.
func (r *Reader) ReadImage(ctx context.Context, snapshot, object string, limit int) (ReaderResult, error) {
	if !r.binary || limit < 0 || limit > 64<<20 || len(snapshot) != 64 || len(object) > 4096 {
		return ReaderResult{}, ErrReader
	}
	if err := ctx.Err(); err != nil {
		return ReaderResult{}, err
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return ReaderResult{}, ctx.Err()
	case <-r.done:
		return ReaderResult{}, ErrReader
	}
	defer func() { <-r.slots }()
	r.mu.Lock()
	r.next++
	id := r.next
	ch := make(chan ReaderResult, 1)
	r.pending[id] = ch
	r.streams[id] = &readerStream{limit: limit, result: ReaderResult{ID: id}}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		if stream := r.streams[id]; stream != nil {
			clear(stream.result.Body)
			stream.result.Body = nil
			stream.canceled = true
		}
		r.mu.Unlock()
		select {
		case leftover := <-ch:
			clear(leftover.Body)
		default:
		}
	}()
	req := struct {
		ID       uint64 `json:"id"`
		Snapshot string `json:"snapshot"`
		Object   string `json:"object"`
		Limit    int    `json:"limit"`
	}{id, snapshot, object, limit}
	if err := r.sendRequest(req); err != nil {
		return ReaderResult{}, ErrReader
	}
	select {
	case result := <-ch:
		if result.Error != "" {
			clear(result.Body)
			return ReaderResult{}, ErrReader
		}
		return result, nil
	case <-ctx.Done():
		_ = r.sendRequest(struct {
			Cancel uint64 `json:"cancel"`
		}{id})
		return ReaderResult{}, ctx.Err()
	case <-r.done:
		return ReaderResult{}, ErrReader
	}
}

func (r *Reader) sendRequest(req any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return json.NewEncoder(r.in).Encode(req)
}

func (r *Reader) receiveStream(in io.Reader) {
	var header [25]byte
	chunk := make([]byte, 256<<10)
	defer clear(chunk)
	ready := false
	for {
		if _, err := io.ReadFull(in, header[:]); err != nil || string(header[:4]) != "WRD2" {
			return
		}
		kind, id := header[4], binary.BigEndian.Uint64(header[5:13])
		size, length := binary.BigEndian.Uint64(header[13:21]), binary.BigEndian.Uint32(header[21:])
		if length > uint32(len(chunk)) || kind < 1 || kind > 5 || (kind != 3 && length != 0) {
			return
		}
		if _, err := io.ReadFull(in, chunk[:length]); err != nil {
			return
		}
		if kind == 1 {
			if ready || id != 0 || size != 0 {
				return
			}
			ready = true
			close(r.ready)
			continue
		}
		if !ready || id == 0 {
			return
		}
		r.mu.Lock()
		valid := r.acceptFrame(kind, id, size, chunk[:length])
		r.mu.Unlock()
		if !valid {
			return
		}
	}
}

// Called with mu held. Receivers copy into pre-bounded private buffers, never
// block a multiplexed connection on an arbitrary external writer.
func (r *Reader) acceptFrame(kind byte, id, size uint64, chunk []byte) bool {
	if id > r.next {
		return false
	}
	stream := r.streams[id]
	if stream == nil {
		return false
	}
	switch kind {
	case 2:
		if stream.started {
			return false
		}
		stream.started = true
		stream.result.Size = size
		if !stream.canceled {
			stream.result.Body = make([]byte, 0, min(uint64(stream.limit), size))
		}
	case 3:
		if !stream.started || size != 0 || len(chunk) == 0 || stream.received+uint64(len(chunk)) > min(uint64(stream.limit), stream.result.Size) {
			return false
		}
		stream.received += uint64(len(chunk))
		if !stream.canceled {
			stream.result.Body = append(stream.result.Body, chunk...)
		}
	case 4, 5:
		if size != 0 || (kind == 4 && (!stream.started || stream.received != min(uint64(stream.limit), stream.result.Size))) {
			return false
		}
		if kind == 5 {
			clear(stream.result.Body)
			stream.result.Body = nil
			stream.result.Error = "source unavailable"
		}
		if ch := r.pending[id]; ch != nil {
			ch <- stream.result
		} else {
			clear(stream.result.Body)
		}
		delete(r.streams, id)
		delete(r.pending, id)
	default:
		return false
	}
	return true
}
