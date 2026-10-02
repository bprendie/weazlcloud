package photoingest

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

func verifyMedia(source io.Reader, expected string) (io.Reader, error) {
	if expected == "application/octet-stream" {
		return source, nil
	}
	prefix := make([]byte, 512)
	n, err := io.ReadFull(source, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	prefix = prefix[:n]
	detected := http.DetectContentType(prefix)
	ok := detected == expected || expected == "video/quicktime" && detected == "video/mp4" || expected == "video/x-matroska" && detected == "video/webm"
	if len(prefix) >= 12 && string(prefix[4:8]) == "ftyp" {
		brand := string(prefix[8:12])
		if expected == "image/heic" {
			ok = brand == "heic" || brand == "heix" || brand == "hevc" || brand == "hevx" || brand == "mif1" || brand == "msf1"
		}
		if expected == "image/avif" {
			ok = brand == "avif" || brand == "avis"
		}
		if expected == "video/quicktime" {
			ok = brand == "qt  " || strings.HasPrefix(brand, "mp4") || brand == "isom"
		}
	}
	if (expected == "image/tiff" || expected == "image/dng") && len(prefix) >= 4 {
		ok = bytes.Equal(prefix[:4], []byte{'I', 'I', 42, 0}) || bytes.Equal(prefix[:4], []byte{'M', 'M', 0, 42})
	}
	if !ok {
		return nil, ErrInvalid
	}
	return io.MultiReader(bytes.NewReader(prefix), source), nil
}
