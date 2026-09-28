package library

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

const (
	FolderPageDefault = 100
	FolderPageMaximum = 200
)

var ErrFolderCursor = errors.New("folder changed; reload the view")

type FolderPage struct {
	Files      []catalog.File `json:"files"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Generation uint64         `json:"generation"`
}

type folderCursor struct {
	Path       string `json:"p"`
	Sort       string `json:"s"`
	Descending bool   `json:"d,omitempty"`
	Generation uint64 `json:"g"`
	Offset     int    `json:"o"`
}

// ListFolderPage serves a stable, bounded list of one directory's children.
func (l *Library) ListFolderPage(ctx context.Context, name, order string, descending bool, limit int, cursor string) (FolderPage, error) {
	if name != "" {
		clean, err := cleanPath(name)
		if err != nil {
			return FolderPage{}, err
		}
		name = clean
	}
	if limit <= 0 {
		limit = FolderPageDefault
	}
	if limit > FolderPageMaximum {
		limit = FolderPageMaximum
	}
	if order != "modified" && order != "size" && order != "type" {
		order = "name"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return FolderPage{}, err
	}
	if name != "" && !l.catalog.IsFolder(name) {
		return FolderPage{}, catalog.ErrNotFound
	}
	generation := l.catalog.Version()
	offset := 0
	if cursor != "" {
		position, err := decodeFolderCursor(cursor)
		if err != nil || position.Path != name || position.Sort != order || position.Descending != descending || position.Generation != generation || position.Offset < 0 {
			return FolderPage{}, ErrFolderCursor
		}
		offset = position.Offset
	}
	rows := l.catalog.Children(name)
	sortFolderRows(rows, order, descending)
	if offset > len(rows) {
		return FolderPage{}, ErrFolderCursor
	}
	end := min(offset+limit, len(rows))
	out := FolderPage{Files: rows[offset:end], Generation: generation}
	if end < len(rows) {
		out.NextCursor = encodeFolderCursor(folderCursor{Path: name, Sort: order, Descending: descending, Generation: generation, Offset: end})
	}
	return out, nil
}

func sortFolderRows(rows []catalog.File, order string, descending bool) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Folder != b.Folder {
			return a.Folder
		}
		switch order {
		case "modified":
			if !a.Mtime.Equal(b.Mtime) {
				return (a.Mtime.Before(b.Mtime)) != descending
			}
		case "size":
			if a.Size != b.Size {
				return (a.Size < b.Size) != descending
			}
		case "type":
			aa, bb := strings.ToLower(path.Ext(a.Path)), strings.ToLower(path.Ext(b.Path))
			if aa != bb {
				return (aa < bb) != descending
			}
		}
		an, bn := strings.ToLower(path.Base(a.Path)), strings.ToLower(path.Base(b.Path))
		if an != bn {
			return (an < bn) != descending
		}
		return a.Path < b.Path
	}
	sort.Slice(rows, less)
}

func encodeFolderCursor(cursor folderCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeFolderCursor(value string) (folderCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return folderCursor{}, err
	}
	var cursor folderCursor
	err = json.Unmarshal(raw, &cursor)
	return cursor, err
}
