package music

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRealEmbeddedTags(t *testing.T) {
	for _, ext := range []string{"mp3", "flac", "m4a", "ogg", "opus"} {
		t.Run(ext, func(t *testing.T) {
			f, err := os.Open("testdata/tagged." + ext)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			tags, err := Read(f)
			if err != nil {
				t.Fatal(err)
			}
			if tags.Title != "Midnight <Signal>" || tags.Artist != "Weazl Test Artist" || tags.Album != "Purple Test Album" || tags.Genre != "Electronic" || tags.Year != "2026" || tags.Track != "3" || len(tags.Picture) == 0 {
				t.Fatalf("tags=%+v", tags)
			}
		})
	}
}

type endlessAudio struct{ read int64 }

func (r *endlessAudio) Read(p []byte) (int, error) {
	clear(p)
	r.read += int64(len(p))
	return len(p), nil
}

func TestHugeAudioDoesNotNeedToBeRead(t *testing.T) {
	b, err := os.ReadFile("testdata/tagged.mp3")
	if err != nil {
		t.Fatal(err)
	}
	tail := &endlessAudio{}
	if _, err = Read(io.MultiReader(bytes.NewReader(b), io.LimitReader(tail, 700<<30))); err != nil {
		t.Fatal(err)
	}
	if tail.read != 0 {
		t.Fatalf("read %d bytes of audio tail", tail.read)
	}
}

func TestMalformedAndOversizedTags(t *testing.T) {
	for _, b := range [][]byte{
		[]byte("ID3\x03\x00\x00\x7f\x7f\x7f\x7fpadding"),
		[]byte("ID3\x04\x00\x00\xff\xff\xff\xffpadding"),
		[]byte("fLaC\x86\xff\xff\xffpadding"),
		[]byte("OggS\x01" + strings.Repeat("\x00", 30)),
		[]byte("\x00\x00\x00\x04ftypxxxx"),
	} {
		if _, err := Read(bytes.NewReader(b)); err == nil {
			t.Fatalf("accepted malformed %x", b)
		}
	}
	// ID3v2.2 has six-byte frame headers, not ten.
	b := []byte("ID3\x02\x00\x00\x00\x00\x00\x06TT2\x00\x00\x00")
	if _, err := Read(bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
}

func TestUTF16AndPreferredFrontCover(t *testing.T) {
	if got := textValue([]byte{0xff, 0xfe, 'H', 0, 'i', 0}, 1); got != "Hi" {
		t.Fatal(got)
	}
	var tags Tags
	tags.picture([]byte("back"), false)
	tags.picture([]byte("front"), true)
	tags.picture([]byte("other"), false)
	if string(tags.Picture) != "front" {
		t.Fatal("front cover lost")
	}
}

func TestMP4ExtendedSizeAndNestedBounds(t *testing.T) {
	atom := func(kind string, body []byte) []byte {
		b := make([]byte, 8+len(body))
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], kind)
		copy(b[8:], body)
		return b
	}
	moov := atom("moov", atom("udta", atom("meta", append(make([]byte, 4), atom("ilst", atom("\xa9nam", atom("data", append(make([]byte, 8), []byte("Title")...))))...))))
	ext := make([]byte, 16)
	binary.BigEndian.PutUint32(ext, 1)
	copy(ext[4:], "mdat")
	binary.BigEndian.PutUint64(ext[8:], 20)
	b := append(atom("ftyp", []byte("M4A ")), append(append(ext, []byte("1234")...), moov...)...)
	tags, err := Read(bytes.NewReader(b))
	if err != nil || tags.Title != "Title" {
		t.Fatalf("%+v %v", tags, err)
	}
	bad := atom("moov", []byte{0, 0, 0, 100, 'u', 'd', 't', 'a'})
	if _, err = Read(bytes.NewReader(append(atom("ftyp", []byte("M4A ")), bad...))); err == nil {
		t.Fatal("accepted child beyond parent")
	}
}

func FuzzTags(f *testing.F) {
	for _, ext := range []string{"mp3", "flac", "m4a", "ogg", "opus"} {
		b, err := os.ReadFile("testdata/tagged." + ext)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < MaxMetadata {
			_, _ = Read(bytes.NewReader(b))
		}
	})
}
