package library

import (
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// Catalog timestamps are UTC instants. Photo calendar buckets and display use
// the known capture offset, so a late-night shot does not move to another day.
// An unknown offset stays unknown; it is never replaced with the node's zone.
func photoCaptureTime(file catalog.File) *time.Time {
	if file.CaptureTime == nil {
		return nil
	}
	value := *file.CaptureTime
	if file.CaptureOffsetMinutes != nil {
		value = value.In(time.FixedZone("", *file.CaptureOffsetMinutes*60))
	}
	return &value
}

func photoCaptureDate(file catalog.File, layout string) string {
	value := photoCaptureTime(file)
	if value == nil {
		return ""
	}
	return value.Format(layout)
}
