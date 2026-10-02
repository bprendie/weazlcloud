package library

import (
	"context"
	"encoding/hex"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"os"
	"strconv"
)

func (l *Library) LiveMotion(ctx context.Context, id string, hidden bool) ([]byte, error) {
	ctx, done := l.previewContext(ctx)
	defer done()
	file, err := l.liveMotionFile(ctx, id, hidden)
	if err != nil {
		return nil, err
	}
	fingerprint, err := l.vault.Fingerprint("live-motion-v1", []byte(id+"\x00"+file.Hash+"\x00"+strconv.FormatUint(file.Revision, 10)))
	if err != nil {
		return nil, err
	}
	defer clear(fingerprint)
	key := hex.EncodeToString(fingerprint)
	if cached, mime, ok := l.readThumbnailCache(key); ok && mime == "video/mp4" {
		body := append([]byte(nil), cached...)
		if current, err := l.liveMotionFile(ctx, id, hidden); err != nil || current.EntryID != file.EntryID || current.Hash != file.Hash || current.Revision != file.Revision {
			clear(body)
			if err == nil {
				err = ErrThumbnailUnavailable
			}
			return nil, err
		}
		return body, nil
	}
	if file.Size > 64<<20 {
		return nil, previewrpc.ErrTooLarge
	}
	release, err := previewMemory.acquire(ctx, sourceAllowance(file)+2*file.Size+16<<20)
	if err != nil {
		return nil, err
	}
	defer release()
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	source, err := l.thumbnailSource(ctx, file, file.Size, false)
	if err != nil {
		return nil, err
	}
	defer clear(source)
	var body []byte
	if socket := os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET"); socket != "" {
		body, err = previewrpc.LiveRequest(ctx, socket, "/v1/live-motion", source)
	} else {
		body, err = RenderLiveMotion(ctx, source)
	}
	if err != nil {
		return nil, err
	}
	if err = l.validatePreview(ctx, file); err != nil {
		clear(body)
		return nil, err
	}
	current, err := l.liveMotionFile(ctx, id, hidden)
	if err != nil || current.EntryID != file.EntryID || current.Hash != file.Hash || current.Revision != file.Revision {
		clear(body)
		if err == nil {
			err = ErrThumbnailUnavailable
		}
		return nil, err
	}
	if err = l.writeThumbnailCache(key, thumbnailEnvelope{Body: body, ContentType: "video/mp4"}); err != nil {
		clear(body)
		return nil, err
	}
	return body, nil
}
