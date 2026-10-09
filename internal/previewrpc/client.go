package previewrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	maxInput  = 64 << 20
	maxOutput = 8 << 20
)

var (
	ErrUnavailable = errors.New("photo worker unavailable")
	ErrRejected    = errors.New("photo worker rejected media")
	ErrTooLarge    = errors.New("photo source exceeds worker bounds")
	transports     sync.Map
)

func Ready(socket string) error {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return conn.Close()
}

type result struct {
	body []byte
	mime string
}

func Render(ctx context.Context, socket string, source []byte, size int, media string) ([]byte, string, error) {
	if len(source) == 0 || len(source) > InputLimit(media) || size < 96 || size > 1280 {
		return nil, "", errors.New("photo preview request is outside supported bounds")
	}
	if media != "raster" && media != "heif" && media != "video" && media != "native" {
		return nil, "", errors.New("unsupported photo worker media type")
	}
	client := workerClient(socket)
	requestURL := "http://photo-worker/v1/thumbnail?size=" + strconv.Itoa(size) + "&media=" + media
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(source))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOutput+1))
	if err != nil || len(body) > maxOutput {
		return nil, "", ErrUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", ResponseError(response)
	}
	mime := response.Header.Get("Content-Type")
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, "", errors.New("photo worker returned an unsupported preview")
	}
	return body, mime, nil
}

func workerClient(socket string) *http.Client {
	if client, ok := transports.Load(socket); ok {
		return client.(*http.Client)
	}
	transport := &http.Transport{
		DisableKeepAlives: false,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second}
	actual, _ := transports.LoadOrStore(socket, client)
	return actual.(*http.Client)
}
