package catalog

import (
	"path"
	"strings"
)

func photoAlbumMember(file File) bool {
	if !file.Present || file.Folder || file.PhotoParentID != "" || !strings.HasPrefix(file.Path, "Photos/") {
		return false
	}
	if len(file.PhotoComponents) > 0 {
		return true
	}
	switch strings.ToLower(path.Ext(file.Path)) {
	case ".dng", ".opaque", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif", ".avif", ".tif", ".tiff", ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return true
	}
	return false
}
