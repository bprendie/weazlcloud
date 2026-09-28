package library

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type SearchOptions struct {
	Query, Scope, Type, Date, Size string
	Sort                           string
	Descending                     bool
	Limit                          int
	Cursor                         string
}

type searchCursor struct {
	SearchOptions
	Generation uint64 `json:"g"`
	Offset     int    `json:"o"`
}

var ErrSearchCursor = errors.New("search changed; reload results")
var ErrSearchQuery = errors.New("search query is too long")

var searchTypes = map[string]map[string]bool{
	"image":    setOf("jpg jpeg png gif webp svg heic avif"),
	"video":    setOf("mp4 mov webm mkv avi m4v"),
	"audio":    setOf("mp3 wav flac m4a aac ogg oga opus"),
	"document": setOf("md txt csv json xml log pdf doc docx xls xlsx ppt pptx odt ods odp"),
	"archive":  setOf("zip tar gz 7z rar iso"),
}

// SearchPage applies the Library filters on the node and returns bounded results.
func (l *Library) SearchPage(ctx context.Context, options SearchOptions) (FolderPage, error) {
	options.Query = strings.ToLower(strings.TrimSpace(options.Query))
	if len(options.Query) > 256 {
		return FolderPage{}, ErrSearchQuery
	}
	if options.Scope != "" {
		clean, err := cleanPath(options.Scope)
		if err != nil {
			return FolderPage{}, err
		}
		options.Scope = clean
	}
	requestCursor := options.Cursor
	options.Cursor = ""
	if options.Limit <= 0 {
		options.Limit = FolderPageDefault
	}
	if options.Limit > FolderPageMaximum {
		options.Limit = FolderPageMaximum
	}
	if options.Sort != "modified" && options.Sort != "size" && options.Sort != "type" {
		options.Sort = "name"
	}
	if options.Type == "" {
		options.Type = "all"
	}
	if options.Date == "" {
		options.Date = "all"
	}
	if options.Size == "" {
		options.Size = "all"
	}
	var pattern *regexp.Regexp
	if strings.ContainsAny(options.Query, "*?") {
		quoted := regexp.QuoteMeta(options.Query)
		quoted = strings.ReplaceAll(strings.ReplaceAll(quoted, `\*`, ".*"), `\?`, ".")
		var err error
		pattern, err = regexp.Compile(quoted)
		if err != nil {
			return FolderPage{}, err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return FolderPage{}, err
	}
	generation := l.catalog.Version()
	offset := 0
	if requestCursor != "" {
		position, err := decodeSearchCursor(requestCursor)
		if err != nil || position.SearchOptions != options || position.Generation != generation || position.Offset < 0 {
			return FolderPage{}, ErrSearchCursor
		}
		offset = position.Offset
	}
	now := time.Now()
	rows, err := l.catalog.Matching(ctx, func(file catalog.File) bool { return matchesSearch(file, options, pattern, now) })
	if err != nil {
		return FolderPage{}, err
	}
	sortFolderRows(rows, options.Sort, options.Descending)
	if offset > len(rows) {
		return FolderPage{}, ErrSearchCursor
	}
	end := min(offset+options.Limit, len(rows))
	page := FolderPage{Files: rows[offset:end], Generation: generation}
	if end < len(rows) {
		page.NextCursor = encodeSearchCursor(searchCursor{SearchOptions: options, Generation: generation, Offset: end})
	}
	return page, nil
}

func matchesSearch(file catalog.File, options SearchOptions, pattern *regexp.Regexp, now time.Time) bool {
	if file.Folder {
		return false
	}
	if options.Scope != "" {
		parent := path.Dir(file.Path)
		if parent != options.Scope && !strings.HasPrefix(parent, options.Scope+"/") {
			return false
		}
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(file.Path)), ".")
	kind := strings.ToLower(ext[:min(3, len(ext))])
	text := strings.ToLower(path.Base(file.Path) + " " + file.Path + " " + kind + " " + ext)
	if options.Query != "" {
		if pattern != nil && !pattern.MatchString(text) {
			return false
		}
		if pattern == nil && !strings.Contains(text, options.Query) {
			return false
		}
	}
	if options.Type != "all" && !searchTypeMatches(options.Type, ext) {
		return false
	}
	if options.Size != "all" {
		switch options.Size {
		case "small":
			if file.Size >= 1<<20 {
				return false
			}
		case "medium":
			if file.Size < 1<<20 || file.Size >= 100<<20 {
				return false
			}
		case "large":
			if file.Size < 100<<20 {
				return false
			}
		}
	}
	if options.Date != "all" {
		var days time.Duration
		switch options.Date {
		case "7d":
			days = 7 * 24 * time.Hour
		case "30d":
			days = 30 * 24 * time.Hour
		case "365d":
			days = 365 * 24 * time.Hour
		}
		if days == 0 || file.Mtime.Before(now.Add(-days)) {
			return false
		}
	}
	return true
}

func searchTypeMatches(kind, ext string) bool {
	return searchTypes[kind][ext]
}

func setOf(values string) map[string]bool {
	set := make(map[string]bool)
	for _, value := range strings.Fields(values) {
		set[value] = true
	}
	return set
}

func encodeSearchCursor(cursor searchCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeSearchCursor(encoded string) (searchCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return searchCursor{}, err
	}
	var cursor searchCursor
	err = json.Unmarshal(raw, &cursor)
	return cursor, err
}
