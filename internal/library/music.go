package library

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/music"
)

type MusicPreview struct {
	music.Tags
	Artwork string `json:"artwork,omitempty"`
}

// Music returns owner-private embedded tags and a small cover, using the same
// encrypted, bounded disk cache and concurrent worker limit as image previews.
func (l *Library) Music(ctx context.Context, name string) (MusicPreview, error) {
	var result MusicPreview
	name, err := cleanPath(name)
	if err != nil {
		return result, err
	}
	f, err := l.Metadata(ctx, name)
	if err != nil {
		return result, err
	}
	if f.Folder {
		return result, ErrThumbnailUnavailable
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".flac", ".m4a", ".ogg", ".oga", ".opus", ".aac", ".wav":
	default:
		return result, ErrThumbnailUnavailable
	}
	key := "music-v1-" + thumbnailKey(name, f, 320)
	if body, _, ok := l.readThumbnailCache(key); ok && json.Unmarshal(body, &result) == nil {
		return result, nil
	}
	l.thumbMu.Lock()
	if job := l.thumbJobs[key]; job != nil {
		l.thumbMu.Unlock()
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-job.done:
			if job.err != nil {
				return result, job.err
			}
			err = json.Unmarshal(job.body, &result)
			return result, err
		}
	}
	job := &thumbnailJob{done: make(chan struct{})}
	l.thumbJobs[key] = job
	l.thumbMu.Unlock()
	defer l.finishThumbnailJob(key, job)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		job.err = ctx.Err()
		return result, job.err
	}
	result, err = l.readMusic(ctx, f)
	if err != nil {
		job.err = err
		return result, err
	}
	job.body, job.err = json.Marshal(result)
	if job.err == nil {
		_ = l.writeThumbnailCache(key, thumbnailEnvelope{ContentType: "application/json", Body: job.body})
	}
	return result, job.err
}

func (l *Library) readMusic(ctx context.Context, f catalog.File) (MusicPreview, error) {
	var result MusicPreview
	l.mu.Lock()
	ref, err := l.capture(f)
	var release func()
	if err == nil {
		release, err = l.holdReference(ref)
	}
	l.mu.Unlock()
	if err != nil {
		return result, err
	}
	defer release()
	streamCtx, cancel := context.WithCancel(ctx)
	r, w := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := l.readReference(streamCtx, ref, w)
		_ = w.CloseWithError(err)
	}()
	tags, err := music.Read(r)
	// Closing both the pipe and process context stops reads promptly once the
	// tags end, including restic's stdout copier. No plaintext staging file.
	_ = r.Close()
	cancel()
	<-done
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, music.ErrUnavailable) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return result, nil
		}
		return result, err
	}
	result.Tags = tags
	result.Picture = nil
	if len(tags.Picture) == 0 {
		return result, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(tags.Picture))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return result, nil
	}
	if body, contentType, err := makeThumbnail(tags.Picture, 320); err == nil {
		result.Artwork = "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body)
	}
	return result, nil
}
