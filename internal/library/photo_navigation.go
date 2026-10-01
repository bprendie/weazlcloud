package library

import (
	"context"
	"sort"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

type PhotoNavigationRequest struct {
	Scope  PhotoScope
	At     string
	Rank   *int
	Around string
	Cursor string
	Limit  int
}
type PhotoNavigationPage struct {
	PhotoPage
	AnchorID string `json:"anchor_id,omitempty"`
	Position int    `json:"position"`
	Start    int    `json:"start"`
	Total    int    `json:"total"`
}
type PhotoNavigationBucket struct {
	Month string `json:"month"`
	Count int    `json:"count"`
	Rank  int    `json:"rank"`
}
type PhotoNavigationDates struct {
	Months       []PhotoNavigationBucket `json:"months"`
	Days         []PhotoNavigationBucket `json:"days,omitempty"`
	UnknownDates int                     `json:"unknown_dates"`
	KnownDates   int                     `json:"known_dates"`
	Total        int                     `json:"total"`
	Generation   uint64                  `json:"generation"`
}
type photoNavigationProjection struct {
	View        photoQueryView
	Dates       PhotoNavigationDates
	Days        []string
	DayRanks    map[string]int
	DayCounts   map[string]int
	UnknownRank int
}

// Caller holds Library.mu and photoMu. Cache is private, generation-bound and
// bounded to sixteen scopes; it stores positions rather than copying records.
func (l *Library) navigationProjectionLocked(scope PhotoScope, key string, view photoQueryView) *photoNavigationProjection {
	if l.photoNavigationEpoch != l.photoEpoch || l.photoNavigationCache == nil {
		l.photoNavigationCache = map[string]*photoNavigationProjection{}
		l.photoNavigationEpoch = l.photoEpoch
	}
	if cached := l.photoNavigationCache[key]; cached != nil {
		return cached
	}
	p := &photoNavigationProjection{View: view, DayRanks: map[string]int{}, DayCounts: map[string]int{}, UnknownRank: -1, Dates: PhotoNavigationDates{Months: []PhotoNavigationBucket{}, Total: view.Len(), Generation: l.photoEpoch}}
	counts := map[string]int{}
	ranks := map[string]int{}
	for i := 0; i < view.Len(); i++ {
		file := view.At(i)
		day := photoCaptureDate(file, "2006-01-02")
		if day == "" {
			p.Dates.UnknownDates++
			if p.UnknownRank < 0 {
				p.UnknownRank = i
			}
			continue
		}
		p.Dates.KnownDates++
		p.DayCounts[day]++
		if _, ok := p.DayRanks[day]; !ok {
			p.DayRanks[day] = i
			p.Days = append(p.Days, day)
		}
		month := day[:7]
		counts[month]++
		if _, ok := ranks[month]; !ok {
			ranks[month] = i
		}
	}
	sort.Strings(p.Days)
	months := []string{}
	for month := range counts {
		months = append(months, month)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))
	for _, month := range months {
		p.Dates.Months = append(p.Dates.Months, PhotoNavigationBucket{Month: month, Count: counts[month], Rank: ranks[month]})
	}
	if len(l.photoNavigationCache) >= 16 {
		l.photoNavigationCache = map[string]*photoNavigationProjection{}
	}
	l.photoNavigationCache[key] = p
	return p
}
func (p *photoNavigationProjection) seekDay(day string) int {
	if day == "unknown" {
		if p.UnknownRank >= 0 {
			return p.UnknownRank
		}
		return -1
	}
	if len(p.Days) == 0 {
		return -1
	}
	index := sort.SearchStrings(p.Days, day)
	if index == len(p.Days) {
		index--
	} else if index > 0 && p.Days[index] != day {
		requested, _ := time.Parse("2006-01-02", day)
		newer, _ := time.Parse("2006-01-02", p.Days[index])
		older, _ := time.Parse("2006-01-02", p.Days[index-1])
		if requested.Sub(older) < newer.Sub(requested) {
			index--
		}
	}
	return p.DayRanks[p.Days[index]]
}
func (l *Library) PhotoNavigation(ctx context.Context, request PhotoNavigationRequest) (PhotoNavigationPage, error) {
	destinations := 0
	for _, present := range []bool{request.At != "", request.Rank != nil, request.Around != "", request.Cursor != ""} {
		if present {
			destinations++
		}
	}
	if destinations > 1 || len(request.Around) > 128 || len(request.Cursor) > 8192 {
		return PhotoNavigationPage{}, ErrPhotoSearch
	}
	scope, pattern, key, err := normalizePhotoScope(request.Scope)
	if err != nil {
		return PhotoNavigationPage{}, err
	}
	if request.At != "" && request.At != "unknown" && !validPhotoSearchDate(request.At) {
		return PhotoNavigationPage{}, ErrPhotoSearch
	}
	if request.Limit <= 0 {
		request.Limit = 100
	}
	request.Limit = min(request.Limit, 200)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoNavigationPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoNavigationPage{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	if l.photoSortedEpoch != l.photoEpoch {
		sortPhotoRows(l.photoMediaRows)
		l.reindexPhotoMediaLocked()
		l.photoSortedEpoch = l.photoEpoch
	}
	view, err := l.navigationViewLocked(scope, pattern)
	if err != nil {
		return PhotoNavigationPage{}, err
	}
	projection := l.navigationProjectionLocked(scope, key, view)
	position := 0
	beforeEnd := -1
	if request.At != "" {
		position = projection.seekDay(request.At)
	}
	if request.Rank != nil {
		position = max(0, min(*request.Rank, view.Len()-1))
	}
	if request.Around != "" {
		position = view.Index(request.Around)
		if position < 0 {
			return PhotoNavigationPage{}, ErrPhotoCursorStale
		}
	}
	start := max(0, position-request.Limit/2)
	if request.Cursor != "" {
		cursor, err := l.decodePhotoCursor(request.Cursor)
		if err != nil || cursor.Scope != key || cursor.Mode != "navigation" {
			return PhotoNavigationPage{}, ErrPhotoCursorStale
		}
		at := view.Index(cursor.AfterID)
		if cursor.BeforeID != "" {
			at = view.Index(cursor.BeforeID)
		}
		if at < 0 {
			at = view.Seek(cursor.Anchor, "all")
			if at < 0 {
				return PhotoNavigationPage{}, ErrPhotoCursorStale
			}
			if cursor.AfterID != "" {
				at--
			}
		}
		if cursor.BeforeID != "" {
			start = max(0, at-request.Limit)
			beforeEnd = at
		} else {
			start = at + 1
		}
		position = start
	}
	end := min(start+request.Limit, view.Len())
	if beforeEnd >= 0 {
		end = min(end, beforeEnd)
	}
	start = min(start, end)
	page := PhotoNavigationPage{PhotoPage: PhotoPage{Items: []PhotoItem{}, Generation: l.photoEpoch, IndexReady: true, Indexed: len(l.photoRows), IndexTotal: len(l.photoRows)}, Position: max(0, position), Start: start, Total: view.Len()}
	if position < 0 {
		return page, nil
	}
	for i := start; i < end; i++ {
		page.Items = append(page.Items, l.photoItemVisibleLocked(view.At(i), scope.Mode == "hidden"))
	}
	if position < view.Len() {
		page.AnchorID = view.At(position).EntryID
	}
	if end < view.Len() && end > start {
		file := view.At(end - 1)
		page.NextCursor, err = l.encodePhotoCursor(photoCursor{Generation: l.photoEpoch, Mode: "navigation", Scope: key, AfterID: file.EntryID, Anchor: anchorOfPhoto(file)})
	}
	if start > 0 {
		file := view.At(start)
		page.PreviousCursor, err = l.encodePhotoCursor(photoCursor{Generation: l.photoEpoch, Mode: "navigation", Scope: key, BeforeID: file.EntryID, Anchor: anchorOfPhoto(file)})
	}
	return page, err
}
func (l *Library) PhotoNavigationSummary(ctx context.Context, scope PhotoScope, month string) (PhotoNavigationDates, error) {
	scope, pattern, key, err := normalizePhotoScope(scope)
	if err != nil {
		return PhotoNavigationDates{}, err
	}
	if month != "" && (len(month) != 7 || !PhotoDateFilter(month)) {
		return PhotoNavigationDates{}, ErrPhotoSearch
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoNavigationDates{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoNavigationDates{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	if l.photoSortedEpoch != l.photoEpoch {
		sortPhotoRows(l.photoMediaRows)
		l.reindexPhotoMediaLocked()
		l.photoSortedEpoch = l.photoEpoch
	}
	view, err := l.navigationViewLocked(scope, pattern)
	if err != nil {
		return PhotoNavigationDates{}, err
	}
	p := l.navigationProjectionLocked(scope, key, view)
	summary := p.Dates
	summary.Months = append([]PhotoNavigationBucket{}, summary.Months...)
	if month != "" {
		for _, day := range p.Days {
			if day[:7] == month {
				summary.Days = append(summary.Days, PhotoNavigationBucket{Month: day, Count: p.DayCounts[day], Rank: p.DayRanks[day]})
			}
		}
	}
	return summary, nil
}
