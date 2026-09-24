package music

import (
	"bytes"
	"encoding/binary"
	"io"
)

func syncSize(b []byte) int {
	n := 0
	for _, v := range b {
		if v&0x80 != 0 {
			return -1
		}
		n = n<<7 | int(v)
	}
	return n
}

func unsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xff && i+1 < len(b) && b[i+1] == 0 {
			i++
		}
	}
	return out
}

func readID3(r io.Reader) (t Tags, err error) {
	var h [10]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	v := h[3]
	if v < 2 || v > 4 || (v == 2 && h[5]&0x40 != 0) {
		return t, ErrUnavailable
	}
	budget := int64(MaxMetadata)
	b, err := take(r, int64(syncSize(h[6:10])), &budget)
	if err != nil {
		return t, err
	}
	if v < 4 && h[5]&0x80 != 0 {
		b = unsync(b)
	}
	if v >= 3 && h[5]&0x40 != 0 {
		if len(b) < 4 {
			return t, ErrUnavailable
		}
		n := int(binary.BigEndian.Uint32(b[:4])) + 4
		if v == 4 {
			n = syncSize(b[:4])
		}
		if n < 4 || n > len(b) {
			return t, ErrUnavailable
		}
		b = b[n:]
	}
	for len(b) > 0 && b[0] != 0 {
		header := 10
		if v == 2 {
			header = 6
		}
		if len(b) < header {
			return t, ErrUnavailable
		}
		id, n := "", 0
		if v >= 3 {
			id, n = string(b[:4]), int(binary.BigEndian.Uint32(b[4:8]))
		}
		if v == 2 {
			id = string(b[:3])
			n = int(b[3])<<16 | int(b[4])<<8 | int(b[5])
		}
		if v == 4 {
			n = syncSize(b[4:8])
		}
		if n < 0 || n > len(b)-header {
			return t, ErrUnavailable
		}
		flags := byte(0)
		if v >= 3 {
			flags = b[9]
		}
		frame := b[header : header+n]
		b = b[header+n:]
		if (v == 3 && flags&0xe0 != 0) || (v == 4 && flags&0x4c != 0) {
			continue
		}
		if v == 4 {
			if flags&2 != 0 || h[5]&0x80 != 0 {
				frame = unsync(frame)
			}
			if flags&1 != 0 {
				if len(frame) < 4 {
					continue
				}
				frame = frame[4:]
			}
		}
		if len(frame) == 0 {
			continue
		}
		keys := map[string]string{"TIT2": "TITLE", "TT2": "TITLE", "TPE1": "ARTIST", "TP1": "ARTIST", "TALB": "ALBUM", "TAL": "ALBUM", "TPE2": "ALBUMARTIST", "TP2": "ALBUMARTIST", "TCON": "GENRE", "TCO": "GENRE", "TYER": "YEAR", "TYE": "YEAR", "TDRC": "DATE", "TRCK": "TRACKNUMBER", "TRK": "TRACKNUMBER"}
		if key := keys[id]; key != "" {
			t.set(key, textValue(frame[1:], frame[0]))
		}
		if id == "APIC" || id == "PIC" {
			readAPIC(&t, frame, id == "PIC")
		}
	}
	return t, nil
}

func readAPIC(t *Tags, b []byte, old bool) {
	enc := b[0]
	b = b[1:]
	if old {
		if len(b) < 3 || string(b[:3]) == "-->" {
			return
		}
		b = b[3:]
	} else {
		i := bytes.IndexByte(b, 0)
		if i < 0 || string(b[:i]) == "-->" {
			return
		}
		b = b[i+1:]
	}
	if len(b) == 0 {
		return
	}
	front := b[0] == 3
	if pic, ok := terminated(b[1:], enc); ok {
		t.picture(pic, front)
	}
}
