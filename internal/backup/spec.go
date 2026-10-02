package backup

import (
	"encoding/hex"
	"path"
	"strings"
	"unicode/utf8"
)

func opaque(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func normalize(spec Spec, device string) (Spec, error) {
	if device != "" {
		if spec.DeviceID != "" && spec.DeviceID != device {
			return Spec{}, ErrNotFound
		}
		spec.DeviceID = device
	}
	if !opaque(spec.DeviceID) || !opaque(spec.SourceID) || !opaque(spec.ItemID) || !opaque(spec.SourceRevision) {
		return Spec{}, ErrInvalid
	}
	p := spec.RelativePath
	if p == "" {
		p = spec.Filename
	}
	if len(p) > 4096 || !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\") || path.IsAbs(p) || strings.TrimSpace(p) != p {
		return Spec{}, ErrInvalid
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return Spec{}, ErrInvalid
		}
	}
	spec.RelativePath, spec.Filename = p, path.Base(p)
	if spec.Kind == "" {
		spec.Kind = "file"
	}
	if spec.Transport == "" {
		spec.Transport = "sequential"
	}
	if spec.Transport != "parts-v1" && spec.Transport != "sequential" {
		return Spec{}, ErrInvalid
	}
	if spec.Size < 0 || spec.Size >= int64(^uint64(0)>>1) {
		return Spec{}, ErrInvalid
	}
	if spec.Kind == "folder" {
		if spec.Size != 0 || spec.SHA256 != "" {
			return Spec{}, ErrInvalid
		}
	} else if spec.Kind == "file" {
		if len(spec.SHA256) != 64 {
			return Spec{}, ErrInvalid
		}
		if _, err := hex.DecodeString(spec.SHA256); err != nil {
			return Spec{}, ErrInvalid
		}
		spec.SHA256 = strings.ToLower(spec.SHA256)
	} else {
		return Spec{}, ErrInvalid
	}
	if (spec.ExpectedEntryID == "") != (spec.ExpectedRevision == 0) {
		return Spec{}, ErrInvalid
	}
	spec.Mtime = spec.Mtime.UTC()
	return spec, nil
}
