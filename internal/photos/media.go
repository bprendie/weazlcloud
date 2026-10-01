package photos

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"
)

type MediaMetadata struct {
	Camera         string
	Width          int
	Height         int
	DurationMillis int64
	Orientation    int
}

func ParseMediaMetadata(name string, raw []byte) (MediaMetadata, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif":
		config, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return MediaMetadata{}, err
		}
		orientation := 1
		if ext == ".jpg" || ext == ".jpeg" {
			if parsed := parseJPEGOrientation(raw); parsed != 0 {
				orientation = parsed
			}
		}
		return MediaMetadata{Width: config.Width, Height: config.Height, Orientation: orientation, Camera: parseJPEGCamera(raw)}, nil
	default:
		return MediaMetadata{}, ErrNoCaptureMetadata
	}
}
