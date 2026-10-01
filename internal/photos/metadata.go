package photos

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoCaptureMetadata = errors.New("capture metadata is unavailable")
	ErrInvalidCapture    = errors.New("capture metadata is invalid")
)

type Capture struct {
	Time          time.Time
	OffsetMinutes *int
	Source        string
	UserCorrected bool
}

// CanonicalCaptureTime is the only date used by the Photos timeline. It
// returns UTC because any known source offset has already been applied while
// parsing; a missing or invalid date remains unknown.
func CanonicalCaptureTime(capture *Capture) (time.Time, bool) {
	if capture == nil || !validCaptureTime(capture.Time) {
		return time.Time{}, false
	}
	return capture.Time.UTC(), true
}

type Candidates struct {
	User     *Capture
	Takeout  *Capture
	Embedded *Capture
	Client   *Capture
}

func Resolve(c Candidates) (Capture, bool) {
	for _, candidate := range []*Capture{c.User, c.Takeout, c.Embedded, c.Client} {
		if candidate == nil || !validCaptureTime(candidate.Time) {
			continue
		}
		result := *candidate
		if c.User == candidate {
			result.UserCorrected = true
		}
		return result, true
	}
	return Capture{}, false
}

func ParseTakeoutSidecar(raw []byte) (Capture, error) {
	var document struct {
		PhotoTakenTime struct {
			Timestamp json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return Capture{}, err
	}
	value := strings.Trim(strings.TrimSpace(string(document.PhotoTakenTime.Timestamp)), "\"")
	if value == "" {
		return Capture{}, ErrNoCaptureMetadata
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < -2208988800 || seconds > 4102444800 {
		return Capture{}, ErrInvalidCapture
	}
	return Capture{Time: time.Unix(seconds, 0).UTC(), Source: "takeout-photoTakenTime"}, nil
}

func ParseEmbedded(name string, raw []byte) (Capture, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return parseJPEGExif(raw)
	default:
		return Capture{}, ErrNoCaptureMetadata
	}
}

func ReadEmbedded(name string, r io.Reader) (Capture, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return Capture{}, err
	}
	return ParseEmbedded(name, bytes.TrimSpace(raw))
}

func normalizeWallTime(value string, offset *int) (time.Time, error) {
	value = strings.TrimSpace(strings.TrimRight(value, "\x00"))
	t, err := time.ParseInLocation("2006:01:02 15:04:05", value, time.UTC)
	if err != nil || !validCaptureTime(t) {
		return time.Time{}, ErrInvalidCapture
	}
	if offset == nil {
		return t, nil
	}
	return t.Add(-time.Duration(*offset) * time.Minute), nil
}

func validCaptureTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1900 && value.Year() <= 2100
}
