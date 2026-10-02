package previewrpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const MaxBundleOutput = (16 << 20) + 4096

type Variant struct {
	Size      int
	Body      []byte
	MIME      string
	ThumbHash []byte
}

func BundleSizes(sizes []int) bool {
	if len(sizes) == 0 || len(sizes) > 8 {
		return false
	}
	seen := map[int]bool{}
	for _, size := range sizes {
		if size < 96 || size > 1280 || seen[size] {
			return false
		}
		seen[size] = true
	}
	return true
}

func WriteVariant(out io.Writer, v Variant) error {
	kind := byte(1)
	if v.MIME == "image/png" {
		kind = 2
	} else if v.MIME != "image/jpeg" {
		return ErrRejected
	}
	return writeFrame(out, uint16(v.Size), kind, v.Body)
}
func WriteHash(out io.Writer, hash []byte) error { return writeFrame(out, 0, 3, hash) }
func EndBundle(out io.Writer, failed bool) error {
	kind := byte(0)
	if failed {
		kind = 255
	}
	return writeFrame(out, 0, kind, nil)
}
func writeFrame(out io.Writer, size uint16, kind byte, body []byte) error {
	if len(body) > maxOutput || (kind == 3 && len(body) > 128) {
		return ErrTooLarge
	}
	var header [7]byte
	binary.BigEndian.PutUint16(header[:2], size)
	header[2] = kind
	binary.BigEndian.PutUint32(header[3:], uint32(len(body)))
	if _, err := out.Write(header[:]); err != nil {
		return err
	}
	_, err := out.Write(body)
	return err
}

func RenderBundle(ctx context.Context, socket string, source []byte, sizes []int, media string, emit func(Variant) error) error {
	if !BundleSizes(sizes) || len(source) == 0 || len(source) > maxInput {
		return ErrTooLarge
	}
	values := make([]string, len(sizes))
	for i, size := range sizes {
		values[i] = strconv.Itoa(size)
	}
	url := "http://photo-worker/v2/thumbnails?sizes=" + strings.Join(values, ",") + "&media=" + media
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(source))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := workerClient(socket).Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		for _, size := range sizes {
			body, mime, err := Render(ctx, socket, source, size, media)
			if err != nil {
				return err
			}
			if err = emit(Variant{Size: size, Body: body, MIME: mime}); err != nil {
				return err
			}
		}
		return nil
	}
	if res.StatusCode != http.StatusOK {
		return ErrRejected
	}
	if res.Header.Get("Content-Type") != "application/vnd.weazl.preview-bundle" {
		return ErrUnavailable
	}
	return ReadBundle(res.Body, sizes, emit)
}

func ReadBundle(input io.Reader, sizes []int, emit func(Variant) error) error {
	if !BundleSizes(sizes) {
		return ErrRejected
	}
	limited := &io.LimitedReader{R: input, N: MaxBundleOutput + 1}
	want := map[int]bool{}
	for _, size := range sizes {
		want[size] = true
	}
	var hash []byte
	for {
		var header [7]byte
		if _, err := io.ReadFull(limited, header[:]); err != nil {
			return ErrUnavailable
		}
		size, kind, n := int(binary.BigEndian.Uint16(header[:2])), header[2], int(binary.BigEndian.Uint32(header[3:]))
		if n > maxOutput || int64(n) > limited.N || limited.N <= 0 {
			return ErrTooLarge
		}
		if kind == 0 || kind == 255 {
			if n != 0 || size != 0 || kind == 255 || len(want) != 0 {
				return ErrRejected
			}
			return nil
		}
		if kind == 3 {
			if size != 0 || len(hash) != 0 || n < 5 || n > 128 {
				return ErrRejected
			}
			hash = make([]byte, n)
			if _, err := io.ReadFull(limited, hash); err != nil {
				return ErrUnavailable
			}
			continue
		}
		if (kind != 1 && kind != 2) || !want[size] || n == 0 {
			return ErrRejected
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(limited, body); err != nil {
			clear(body)
			return ErrUnavailable
		}
		mime := "image/jpeg"
		if kind == 2 {
			mime = "image/png"
		}
		delete(want, size)
		if err := emit(Variant{Size: size, Body: body, MIME: mime, ThumbHash: hash}); err != nil {
			return err
		}
	}
}
