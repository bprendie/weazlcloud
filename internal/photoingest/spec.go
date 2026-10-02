// Package photoingest coordinates logical uploads using the existing durable
// upload engine. Its encrypted receipts contain metadata, never original bytes.
package photoingest

import (
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

var ErrInvalid = errors.New("invalid photo upload specification")

type Component struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type Spec struct {
	SourceNamespace       string      `json:"source_namespace,omitempty"`
	SourceAssetID         string      `json:"source_asset_id,omitempty"`
	SourceMappingRevision uint64      `json:"source_mapping_revision,omitempty"`
	Transport             string      `json:"transport,omitempty"`
	CommitWhenComplete    bool        `json:"commit_when_complete,omitempty"`
	Hidden                bool        `json:"hidden,omitempty"`
	OriginalMode          string      `json:"original_mode,omitempty"`
	DeviceID              string      `json:"device_id"`
	DeviceAssetID         string      `json:"device_asset_id"`
	SourceRevision        string      `json:"source_revision,omitempty"`
	Filename              string      `json:"filename,omitempty"`
	Size                  int64       `json:"size,omitempty"`
	SHA256                string      `json:"sha256,omitempty"`
	RootID                string      `json:"root_id,omitempty"`
	Components            []Component `json:"components,omitempty"`
	AlbumIDs              []string    `json:"album_ids,omitempty"`
	CapturedAt            string      `json:"captured_at,omitempty"`
	OffsetKnown           bool        `json:"offset_known,omitempty"`
}

func (s *Spec) Normalize() error {
	if s.SourceNamespace != "" || s.SourceAssetID != "" {
		if s.SourceNamespace == "" || s.SourceAssetID == "" || len(s.SourceNamespace) > 200 || len(s.SourceAssetID) > 4096 {
			return ErrInvalid
		}
		key := catalog.SafeSourceKey(s.SourceNamespace, s.SourceAssetID)
		if s.DeviceAssetID != "" && s.DeviceAssetID != key {
			return ErrInvalid
		}
		s.DeviceAssetID = key
	}
	if s.Transport != "" && s.Transport != "parts-v1" {
		return ErrInvalid
	}
	if s.OriginalMode != "" && s.OriginalMode != "opaque-original-v1" {
		return ErrInvalid
	}
	if s.SourceRevision == "" {
		s.SourceRevision = "1"
	}
	if s.RootID == "" {
		s.RootID = "root:photos"
	}
	if !ValidPart(s.DeviceID) || !ValidPart(s.DeviceAssetID) || !ValidPart(s.SourceRevision) || len(s.AlbumIDs) > 100 {
		return ErrInvalid
	}
	if len(s.Components) == 0 {
		s.Components = []Component{{ID: "original", Filename: s.Filename, Size: s.Size, SHA256: s.SHA256}}
	} else if s.Filename != "" || s.Size != 0 || s.SHA256 != "" {
		return ErrInvalid
	}
	s.Filename, s.Size, s.SHA256 = "", 0, ""
	if len(s.Components) > 2 {
		return ErrInvalid
	}
	if len(s.Components) == 2 && s.Components[0].ID == "motion" {
		s.Components[0], s.Components[1] = s.Components[1], s.Components[0]
	}
	for i := range s.Components {
		c := &s.Components[i]
		if i == 0 && c.ID != "original" || i == 1 && c.ID != "motion" || c.Filename == "." || c.Filename == ".." || c.Filename == "" || len(c.Filename) > 255 || strings.ContainsAny(c.Filename, "/\\\x00\r\n") || c.Size < 1 || c.Size > (1<<63-1)/2 {
			return ErrInvalid
		}
		c.SHA256 = strings.ToLower(c.SHA256)
		if raw, err := hex.DecodeString(c.SHA256); err != nil || len(raw) != 32 {
			return ErrInvalid
		}
		kind := MediaType(c.Filename)
		if i == 0 && s.OriginalMode == "opaque-original-v1" {
			kind = "application/octet-stream"
		}
		if kind == "" || c.MediaType != "" && c.MediaType != kind {
			return ErrInvalid
		}
		c.MediaType = kind
		if len(s.Components) == 2 && (i == 0 && kind != "application/octet-stream" && !strings.HasPrefix(kind, "image/") || i == 1 && !strings.HasPrefix(kind, "video/")) {
			return ErrInvalid
		}
	}
	if len(s.Components) == 2 && s.Components[0].Filename == s.Components[1].Filename {
		return ErrInvalid
	}
	_, err := s.Capture()
	return err
}

func (s Spec) Capture() (*catalog.CaptureMetadata, error) {
	if s.CapturedAt == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.CapturedAt)
	if err != nil || t.Year() < 1900 || t.Year() > 2100 {
		return nil, ErrInvalid
	}
	capture := &catalog.CaptureMetadata{Time: &t, Source: "client"}
	if s.OffsetKnown {
		_, seconds := t.Zone()
		minutes := seconds / 60
		capture.OffsetMinutes = &minutes
	}
	return capture, nil
}

func ValidPart(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 100 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func MediaType(name string) string {
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
	case ".dng":
		return "image/dng"
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
		return ""
	}
}
