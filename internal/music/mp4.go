package music

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
)

// M4A stores iTunes tags under moov/udta/meta/ilst. If moov follows mdat,
// stream past the audio without retaining it. The caller bounds elapsed time.
func readMP4(r io.Reader) (t Tags, err error) {
	budget := int64(MaxMetadata)
	for i := 0; i < 4096; i++ {
		kind, size, e := atomHeader(r)
		if e != nil {
			return t, e
		}
		if kind == "moov" {
			body, e := take(r, size, &budget)
			if e != nil {
				return t, e
			}
			remaining := 4096
			e = walkAtoms(&t, body, "", 0, &remaining)
			return t, e
		}
		if _, e = io.CopyN(io.Discard, r, size); e != nil {
			return t, e
		}
	}
	return t, ErrUnavailable
}

func atomHeader(r io.Reader) (string, int64, error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return "", 0, err
	}
	kind := string(h[4:])
	size, header := uint64(binary.BigEndian.Uint32(h[:4])), uint64(8)
	if size == 1 {
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return "", 0, err
		}
		size, header = binary.BigEndian.Uint64(h[:]), 16
	}
	if size < header || size > 1<<63-1 {
		return "", 0, ErrUnavailable
	}
	return kind, int64(size - header), nil
}

func walkAtoms(t *Tags, b []byte, parent string, depth int, remaining *int) error {
	if depth > 8 {
		return ErrUnavailable
	}
	r := bytes.NewReader(b)
	for r.Len() > 0 {
		*remaining -= 1
		if *remaining < 0 {
			return ErrUnavailable
		}
		kind, n, err := atomHeader(r)
		if err != nil || n > int64(r.Len()) {
			return ErrUnavailable
		}
		start := len(b) - r.Len()
		body := b[start : start+int(n)]
		_, _ = r.Seek(n, io.SeekCurrent)
		switch kind {
		case "udta", "ilst":
			if err = walkAtoms(t, body, kind, depth+1, remaining); err != nil {
				return err
			}
		case "meta":
			if len(body) < 4 {
				return ErrUnavailable
			}
			if err = walkAtoms(t, body[4:], kind, depth+1, remaining); err != nil {
				return err
			}
		default:
			if parent == "ilst" {
				if err = walkAtoms(t, body, kind, depth+1, remaining); err != nil {
					return err
				}
			} else if kind == "data" && len(body) >= 8 {
				data := body[8:]
				if parent == "covr" {
					t.picture(data, true)
					continue
				}
				if parent == "trkn" && len(data) >= 4 {
					t.set("TRACKNUMBER", strconv.Itoa(int(binary.BigEndian.Uint16(data[2:4]))))
				}
				keys := map[string]string{"\xa9nam": "TITLE", "\xa9ART": "ARTIST", "\xa9art": "ARTIST", "\xa9alb": "ALBUM", "aART": "ALBUMARTIST", "\xa9gen": "GENRE", "\xa9day": "YEAR"}
				if key := keys[parent]; key != "" {
					t.set(key, textValue(data, 3))
				}
			}
		}
	}
	return nil
}
