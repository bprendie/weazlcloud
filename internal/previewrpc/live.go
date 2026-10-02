package previewrpc

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

func LiveRequest(ctx context.Context, socket, path string, source []byte) ([]byte, error) {
	if len(source) == 0 || len(source) > maxInput {
		return nil, ErrTooLarge
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://photo-worker"+path, bytes.NewReader(source))
	if err != nil {
		return nil, err
	}
	res, err := workerClient(socket).Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, ResponseError(res)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxOutput+1))
	if err != nil || len(body) > maxOutput {
		clear(body)
		return nil, ErrTooLarge
	}
	return body, nil
}
