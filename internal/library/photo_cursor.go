package library

import (
	"encoding/base64"
	"encoding/json"
	"path"
	"strings"
)

func (l *Library) encodePhotoCursor(cursor photoCursor) (string, error) {
	plain, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(wrapped), nil
}

func (l *Library) decodePhotoCursor(raw string) (photoCursor, error) {
	var cursor photoCursor
	wrapped, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(wrapped) > 4096 {
		return cursor, ErrPhotoCursor
	}
	plain, err := l.vault.Unwrap(wrapped)
	if err != nil {
		return cursor, ErrPhotoCursor
	}
	defer clear(plain)
	if json.Unmarshal(plain, &cursor) != nil || cursor.Generation == 0 || (cursor.AfterID == "") == (cursor.BeforeID == "") || len(cursor.AfterID) > 128 || len(cursor.BeforeID) > 128 {
		return photoCursor{}, ErrPhotoCursor
	}
	return cursor, nil
}

func photoMediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic", ".heif":
		return "image/heic"
	case ".avif":
		return "image/avif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	default:
		return "application/octet-stream"
	}
}
