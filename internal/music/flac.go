package music

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
)

func readFLAC(r io.Reader) (t Tags, err error) {
	if _, err = io.CopyN(io.Discard, r, 4); err != nil {
		return
	}
	budget := int64(MaxMetadata)
	for i := 0; i < 4096; i++ {
		h, e := take(r, 4, &budget)
		if e != nil {
			return t, e
		}
		n := int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
		b, e := take(r, n, &budget)
		if e != nil {
			return t, e
		}
		switch h[0] & 127 {
		case 4:
			if e = readComments(&t, b); e != nil {
				return t, e
			}
		case 6:
			readPicture(&t, b)
		}
		if h[0]&128 != 0 {
			return t, nil
		}
	}
	return t, ErrUnavailable
}

func sized(b *[]byte, order binary.ByteOrder) ([]byte, bool) {
	if len(*b) < 4 {
		return nil, false
	}
	n := uint64(order.Uint32((*b)[:4]))
	*b = (*b)[4:]
	if n > uint64(len(*b)) {
		return nil, false
	}
	value := (*b)[:n]
	*b = (*b)[n:]
	return value, true
}

func readComments(t *Tags, b []byte) error {
	if _, ok := sized(&b, binary.LittleEndian); !ok || len(b) < 4 {
		return ErrUnavailable
	}
	count := binary.LittleEndian.Uint32(b[:4])
	b = b[4:]
	if count > 65536 {
		return ErrUnavailable
	}
	for i := uint32(0); i < count; i++ {
		value, ok := sized(&b, binary.LittleEndian)
		if !ok {
			return ErrUnavailable
		}
		key, val, ok := strings.Cut(string(value), "=")
		if !ok {
			continue
		}
		if strings.EqualFold(key, "METADATA_BLOCK_PICTURE") {
			if picture, err := base64.StdEncoding.DecodeString(val); err == nil {
				readPicture(t, picture)
			}
		} else {
			t.set(key, textValue([]byte(val), 3))
		}
	}
	return nil
}

func readPicture(t *Tags, b []byte) {
	if len(b) < 4 {
		return
	}
	front := binary.BigEndian.Uint32(b[:4]) == 3
	b = b[4:]
	mime, ok := sized(&b, binary.BigEndian)
	if !ok || string(mime) == "-->" {
		return
	}
	if _, ok = sized(&b, binary.BigEndian); !ok || len(b) < 16 {
		return
	}
	b = b[16:] // Dimensions, depth, indexed colors; verify dimensions when decoding.
	if pic, ok := sized(&b, binary.BigEndian); ok {
		t.picture(pic, front)
	}
}

// Vorbis and Opus comments may span Ogg pages. Only the first logical stream
// is inspected; a total metadata budget bounds packet assembly.
func readOgg(r io.Reader) (t Tags, err error) {
	budget := int64(MaxMetadata)
	var packet []byte
	var serial uint32
	for page := 0; page < 4096; page++ {
		h, e := take(r, 27, &budget)
		if e != nil {
			return t, e
		}
		if string(h[:4]) != "OggS" || h[4] != 0 {
			return t, ErrUnavailable
		}
		if page == 0 {
			serial = binary.LittleEndian.Uint32(h[14:18])
		}
		if serial != binary.LittleEndian.Uint32(h[14:18]) {
			return t, ErrUnavailable
		}
		laces, e := take(r, int64(h[26]), &budget)
		if e != nil {
			return t, e
		}
		for _, n := range laces {
			part, e := take(r, int64(n), &budget)
			if e != nil {
				return t, e
			}
			packet = append(packet, part...)
			if n == 255 {
				continue
			}
			if bytes.HasPrefix(packet, []byte("\x03vorbis")) {
				e = readComments(&t, packet[7:])
				return t, e
			}
			if bytes.HasPrefix(packet, []byte("OpusTags")) {
				e = readComments(&t, packet[8:])
				return t, e
			}
			if !bytes.HasPrefix(packet, []byte("\x01vorbis")) && !bytes.HasPrefix(packet, []byte("OpusHead")) {
				return t, ErrUnavailable
			}
			packet = nil
		}
	}
	return t, ErrUnavailable
}
