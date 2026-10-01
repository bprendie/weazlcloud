package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/vault"
	"sort"
	"time"
)

type PhotoDateBucket struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

type PhotoDateSummary struct {
	Months       []PhotoDateBucket `json:"months"`
	UnknownDates int               `json:"unknown_dates"`
	Generation   uint64            `json:"generation"`
}

// PhotoDateSummary returns compact month counts from the private metadata
// index. It never reads source media bytes.
func (l *Library) PhotoDateSummary(ctx context.Context, hiddenView ...bool) (PhotoDateSummary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoDateSummary{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoDateSummary{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	includeHidden := len(hiddenView) != 0 && hiddenView[0]
	if !includeHidden && l.photoDateSummaryEpoch == l.photoEpoch && l.photoDateSummary.Months != nil {
		cached := l.photoDateSummary
		cached.Months = append([]PhotoDateBucket(nil), cached.Months...)
		return cached, nil
	}
	counts := make(map[string]int)
	unknown := 0
	for _, file := range l.photoMediaRows {
		if l.photoPathHiddenLocked(file.Path) != includeHidden {
			continue
		}
		if !includeHidden && file.Archived {
			continue
		}
		if file.CaptureTime == nil {
			unknown++
			continue
		}
		month := photoCaptureDate(file, "2006-01")
		counts[month]++
	}
	months := make([]string, 0, len(counts))
	for month := range counts {
		months = append(months, month)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))
	result := PhotoDateSummary{Months: make([]PhotoDateBucket, 0, len(months)), UnknownDates: unknown, Generation: l.photoEpoch}
	for _, month := range months {
		result.Months = append(result.Months, PhotoDateBucket{Month: month, Count: counts[month]})
	}
	if !includeHidden {
		l.photoDateSummary, l.photoDateSummaryEpoch = result, l.photoEpoch
	}
	result.Months = append([]PhotoDateBucket(nil), result.Months...)
	return result, nil
}

// PhotoDateFilter validates a month or calendar day for timeline jumps.
func PhotoDateFilter(date string) bool {
	if date == "" || date == "unknown" {
		return true
	}
	layout := "2006-01-02"
	if len(date) == 4 {
		layout = "2006"
	} else if len(date) == 7 {
		layout = "2006-01"
	}
	_, err := time.Parse(layout, date)
	return err == nil
}
