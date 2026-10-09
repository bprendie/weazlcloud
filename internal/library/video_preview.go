package library

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// QuickTime commonly stores its movie index after the media payload. A pipe
// cannot seek back to its frames. Give ffmpeg a bounded anonymous RAM file;
// plaintext never lands in the filesystem. Other raster formats keep pipes.
func previewMediaInput(data []byte, media string) ([]string, []*os.File, func(), error) {
	noop := func() {}
	if media != "video" {
		return []string{"-protocol_whitelist", "pipe,crypto,data", "-i", "pipe:0"}, nil, noop, nil
	}
	if len(data) == 0 || len(data) > thumbnailMaxInput {
		return nil, nil, noop, ErrPreviewTooLarge
	}
	fd, err := unix.MemfdCreate("weazl-video-preview", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, nil, noop, err
	}
	file := os.NewFile(uintptr(fd), "video-preview")
	cleanup := func() { file.Close() }
	if err := writeAll(file, data); err != nil {
		cleanup()
		return nil, nil, noop, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, noop, err
	}
	// Reject playlists and network protocols. MOV external data references are
	// disabled by the demuxer's defaults; do not enable them here.
	return []string{"-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,avi,mpeg,mpegts,ogg", "-i", "/proc/self/fd/3"}, []*os.File{file}, cleanup, nil
}
