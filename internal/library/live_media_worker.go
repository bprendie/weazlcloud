package library

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"golang.org/x/sys/unix"
)

type LivePhotoIdentity struct {
	Identifier string `json:"identifier"`
	MediaType  string `json:"media_type"`
}

func liveMediaFile(data []byte, suffix string) (*os.File, string, func(), error) {
	fd, err := unix.MemfdCreate("weazl-live-source", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, "", func() {}, previewrpc.ErrEnvironment
	}
	f := os.NewFile(uintptr(fd), "live-source")
	cleanup := func() { f.Close() }
	if err := writeAll(f, data); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	f.Seek(0, io.SeekStart)
	dir, err := os.MkdirTemp("", "weazl-live-")
	if err != nil {
		cleanup()
		return nil, "", func() {}, previewrpc.ErrEnvironment
	}
	cleanup = func() { f.Close(); os.RemoveAll(dir) }
	name := filepath.Join(dir, "source"+suffix)
	if err := os.Symlink("/proc/self/fd/3", name); err != nil {
		cleanup()
		return nil, "", func() {}, previewrpc.ErrEnvironment
	}
	return f, name, cleanup, nil
}

// InspectLivePhoto runs only in the isolated worker (or explicit local fallback).
func InspectLivePhoto(parent context.Context, data []byte, media string) (LivePhotoIdentity, error) {
	var result LivePhotoIdentity
	suffix := ".jpg"
	if media == "heif" {
		suffix = ".heic"
	}
	if media == "video" {
		suffix = ".mov"
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	file, name, cleanup, err := liveMediaFile(data, suffix)
	if err != nil {
		return result, err
	}
	defer cleanup()
	tool, err := exec.LookPath("exiftool")
	if err != nil {
		return result, previewrpc.ErrEnvironment
	}
	cmd := exec.CommandContext(ctx, tool, "-j", "-ContentIdentifier", "-MediaGroupUUID", "-MIMEType", name)
	cmd.ExtraFiles = []*os.File{file}
	cmd.WaitDelay = time.Second
	cmd.Stderr = io.Discard
	output := &boundedPreviewWriter{limit: 8192, cancel: cancel}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return result, previewrpc.ErrTimeout
		}
		return result, previewrpc.ErrRejected
	}
	defer clear(output.Bytes())
	var rows []struct {
		ContentIdentifier string
		MediaGroupUUID    string
		MIMEType          string
	}
	if json.Unmarshal(output.Bytes(), &rows) != nil || len(rows) != 1 {
		return result, previewrpc.ErrRejected
	}
	result.Identifier = rows[0].ContentIdentifier
	if result.Identifier == "" {
		result.Identifier = rows[0].MediaGroupUUID
	}
	if len(result.Identifier) > 256 {
		return LivePhotoIdentity{}, previewrpc.ErrRejected
	}
	result.MediaType = rows[0].MIMEType
	return result, nil
}

func RenderLiveMotion(parent context.Context, data []byte) ([]byte, error) {
	release, err := previewMemory.acquire(parent, int64(len(data))*2+256<<20)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	file, name, cleanup, err := liveMediaFile(data, ".mov")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, previewrpc.ErrEnvironment
	}
	cmd := exec.CommandContext(ctx, tool, "-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe,crypto,data", "-i", name, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1", "-t", "10", "-vf", "scale=w='min(1280,iw)':h=-2", "-c:v", "libx264", "-threads", "1", "-preset", "veryfast", "-crf", "24", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "96k", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "pipe:1")
	cmd.ExtraFiles = []*os.File{file}
	cmd.WaitDelay = time.Second
	cmd.Stderr = io.Discard
	output := &boundedPreviewWriter{limit: 8 << 20, cancel: cancel}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		clear(output.Bytes())
		if output.exceeded {
			return nil, previewrpc.ErrTooLarge
		}
		if ctx.Err() != nil {
			return nil, previewrpc.ErrTimeout
		}
		return nil, previewrpc.ErrRejected
	}
	return output.Bytes(), nil
}
