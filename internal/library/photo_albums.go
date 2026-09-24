package library

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

const PhotosRoot = "Google Takeout/Photos/"
const albumMetadataLimit = 1 << 20

var photoYearFolder = regexp.MustCompile(`(?i)^(photos from )?[0-9]{4}$`)

type PhotoAlbum struct {
	Path            string `json:"path"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	Count           int    `json:"count"`
	Cover           string `json:"cover,omitempty"`
	Source          string `json:"source"`
	MetadataWarning bool   `json:"metadata_warning,omitempty"`
}

type albumMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
}

func photoMedia(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif", ".avif", ".tif", ".tiff", ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return true
	}
	return false
}

func albumMetadataName(name string) bool {
	switch strings.ToLower(name) {
	case "metadata.json", "métadonnées.json", "metadatos.json", "metadades.json", "metadati.json", "metadáta.json", "metadaten.json", "метаданные.json", "метаданни.json":
		return true
	}
	return false
}

func parseAlbumMetadata(raw []byte) albumMetadata {
	var doc struct {
		Title          string          `json:"title"`
		Description    string          `json:"description"`
		AlbumData      *albumMetadata  `json:"albumData"`
		PhotoTakenTime json.RawMessage `json:"photoTakenTime"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.PhotoTakenTime) != 0 {
		return albumMetadata{}
	}
	meta := albumMetadata{Title: doc.Title, Description: doc.Description}
	if doc.AlbumData != nil {
		meta = *doc.AlbumData
	}
	meta.Title = strings.TrimSpace(meta.Title)
	meta.Valid = meta.Title != "" || meta.Description != "" || doc.AlbumData != nil
	return meta
}

// PhotoAlbums derives membership from the live private catalog. It works for
// earlier imports, split ZIPs and restarts without a second membership database.
// Only small album metadata files are read; originals and photo sidecars are not.
func (l *Library) PhotoAlbums(ctx context.Context) ([]PhotoAlbum, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	groups := make(map[string]*PhotoAlbum)
	metadata := make(map[string]catalog.File)
	for _, f := range l.catalog.List() {
		if !strings.HasPrefix(f.Path, PhotosRoot) {
			continue
		}
		rel := strings.TrimPrefix(f.Path, PhotosRoot)
		folder, child, nested := strings.Cut(rel, "/")
		if folder == "" || (!nested && !f.Folder) {
			continue
		}
		albumPath := PhotosRoot + folder
		album := groups[albumPath]
		if album == nil {
			album = &PhotoAlbum{Path: albumPath, Title: folder, Source: "folder"}
			groups[albumPath] = album
		}
		if f.Folder {
			continue
		}
		if !strings.Contains(child, "/") && albumMetadataName(child) {
			old, exists := metadata[albumPath]
			if !exists || f.Path < old.Path {
				metadata[albumPath] = f
			}
		}
		if photoMedia(f.Path) {
			album.Count++
			// Prefer a still image; videos remain playable inside the album.
			if !isPhotoVideo(f.Path) && (album.Cover == "" || f.Path < album.Cover) {
				album.Cover = f.Path
			}
		}
	}
	l.loadAlbumCache()
	nextCache := make(map[string]albumMetadata)
	out := make([]PhotoAlbum, 0, len(groups))
	for folder, album := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f, ok := metadata[folder]; ok {
			key := f.Path + ":" + f.Hash
			meta, cached := l.albumMetadata[key]
			if !cached && f.Size >= 0 && f.Size <= albumMetadataLimit {
				ref, err := l.capture(f)
				if err == nil {
					var raw bytes.Buffer
					if err = l.readReferenceRange(ctx, ref, 0, f.Size, &raw); err == nil {
						meta = parseAlbumMetadata(raw.Bytes())
						cached = true
					}
				}
			}
			if cached && len(nextCache) < 2048 {
				nextCache[key] = meta
			}
			if meta.Valid {
				if meta.Title != "" {
					album.Title = meta.Title
				}
				album.Description, album.Source = meta.Description, "metadata"
			} else {
				album.MetadataWarning = true
			}
		}
		name := path.Base(folder)
		if strings.EqualFold(name, "Trash") || strings.EqualFold(name, "Failed Videos") || (photoYearFolder.MatchString(name) && (album.Source != "metadata" || photoYearFolder.MatchString(album.Title))) {
			continue
		}
		out = append(out, *album)
	}
	changed := len(l.albumMetadata) != len(nextCache)
	for key, meta := range nextCache {
		if old, ok := l.albumMetadata[key]; !ok || old != meta {
			changed = true
			break
		}
	}
	l.albumMetadata = nextCache
	if changed {
		l.saveAlbumCache()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title == out[j].Title {
			return out[i].Path < out[j].Path
		}
		return out[i].Title < out[j].Title
	})
	return out, nil
}

func isPhotoVideo(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return true
	}
	return false
}
