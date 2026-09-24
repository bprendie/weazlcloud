// Package music reads embedded tags without buffering the audio payload.
package music

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

const MaxMetadata = 8 << 20

var ErrUnavailable = errors.New("music tags unavailable")

type Tags struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"album_artist,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        string `json:"year,omitempty"`
	Track       string `json:"track,omitempty"`
	Picture     []byte `json:"-"`
	front       bool
}

// Read stops at the end of the tags. The caller must close/cancel its source.
// Allocations follow validated metadata lengths, never the audio file size.
func Read(src io.Reader) (Tags, error) {
	r := bufio.NewReaderSize(src, 4096)
	h, err := r.Peek(12)
	if err != nil {
		return Tags{}, err
	}
	switch {
	case string(h[:3]) == "ID3":
		return readID3(r)
	case string(h[:4]) == "fLaC":
		return readFLAC(r)
	case string(h[:4]) == "OggS":
		return readOgg(r)
	case string(h[4:8]) == "ftyp":
		return readMP4(r)
	default:
		return Tags{}, ErrUnavailable
	}
}

func take(r io.Reader, n int64, budget *int64) ([]byte, error) {
	if n < 0 || n > *budget {
		return nil, ErrUnavailable
	}
	*budget -= n
	b := make([]byte, int(n))
	_, err := io.ReadFull(r, b)
	return b, err
}

func (t *Tags) set(key, value string) {
	value = strings.TrimSpace(strings.Trim(strings.ToValidUTF8(value, "�"), "\x00"))
	if len(value) > 1024 {
		value = string([]rune(value)[:min(256, len([]rune(value)))])
	}
	switch strings.ToUpper(key) {
	case "TITLE":
		t.Title = value
	case "ARTIST":
		t.Artist = value
	case "ALBUM":
		t.Album = value
	case "ALBUMARTIST", "ALBUM_ARTIST":
		t.AlbumArtist = value
	case "GENRE":
		t.Genre = value
	case "DATE", "YEAR":
		t.Year = value
	case "TRACKNUMBER":
		t.Track = value
	}
}

func (t *Tags) picture(data []byte, front bool) {
	if len(data) == 0 || len(data) > MaxMetadata || (len(t.Picture) > 0 && (!front || t.front)) {
		return
	}
	t.Picture, t.front = data, front
}

func textValue(b []byte, encoding byte) string {
	if len(b) > 4096 {
		b = b[:4096]
	}
	switch encoding {
	case 0:
		runes := make([]rune, len(b))
		for i, v := range b {
			runes[i] = rune(v)
		}
		return string(runes)
	case 1, 2:
		var order binary.ByteOrder = binary.BigEndian
		if bytes.HasPrefix(b, []byte{0xff, 0xfe}) {
			order = binary.LittleEndian
			b = b[2:]
		} else if bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
			b = b[2:]
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = order.Uint16(b[i*2:])
		}
		return string(utf16.Decode(u))
	case 3:
		return string(b)
	}
	return ""
}

func terminated(b []byte, enc byte) ([]byte, bool) {
	step := 1
	if enc == 1 || enc == 2 {
		step = 2
	}
	for i := 0; i+step <= len(b); i += step {
		if b[i] == 0 && (step == 1 || b[i+1] == 0) {
			return b[i+step:], true
		}
	}
	return nil, false
}
