package library

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

type metadataSidecars struct {
	index    photos.SidecarIndex
	captures map[string]photos.Capture
	errors   map[string]error
}
type metadataResolver struct {
	lib         *Library
	files       map[string]catalog.File
	directories map[string][]string
	cache       map[string]*metadataSidecars
}

func (l *Library) newMetadataResolver(ctx context.Context) (*metadataResolver, error) {
	r := &metadataResolver{lib: l, files: map[string]catalog.File{}, cache: map[string]*metadataSidecars{}}
	if err := r.refresh(ctx); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *metadataResolver) refresh(ctx context.Context) error {
	r.lib.mu.Lock()
	defer r.lib.mu.Unlock()
	if err := r.lib.ensure(ctx); err != nil {
		return err
	}
	files := map[string]catalog.File{}
	directories := map[string][]string{}
	for _, file := range r.lib.catalog.List() {
		if inPhotoRoot(file.Path) && !file.Folder && strings.HasSuffix(strings.ToLower(file.Path), ".json") {
			files[file.Path] = file
			dir := path.Dir(file.Path)
			directories[dir] = append(directories[dir], file.Path)
			old, ok := r.files[file.Path]
			if !ok || old.Hash != file.Hash || old.Revision != file.Revision {
				delete(r.cache, dir)
			}
		}
	}
	for name := range r.files {
		if _, ok := files[name]; !ok {
			delete(r.cache, path.Dir(name))
		}
	}
	r.files, r.directories = files, directories
	return nil
}

func (r *metadataResolver) read(ctx context.Context, file catalog.File, limit int64) ([]byte, error) {
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release, err := previewMemory.acquireBackground(ctx, sourceAllowance(file)+2*limit)
	if err != nil {
		return nil, err
	}
	defer release()
	return r.lib.thumbnailSource(ctx, file, min(file.Size, limit), file.Size > limit)
}

func (r *metadataResolver) sidecar(ctx context.Context, name string) (photos.Capture, error) {
	dir, base := path.Dir(name), path.Base(name)
	var exact []string
	for _, suffix := range []string{".json", ".supplemental-metadata.json"} {
		if _, ok := r.files[name+suffix]; ok {
			exact = append(exact, name+suffix)
		}
	}
	if len(exact) > 1 {
		return photos.Capture{}, photos.ErrAmbiguousSidecar
	}
	if len(exact) == 1 {
		return r.readSidecar(ctx, exact[0], base)
	}
	cached := r.cache[dir]
	if cached == nil {
		if len(r.directories[dir]) > 10000 {
			return photos.Capture{}, errors.New("sidecar directory exceeds candidate limit")
		}
		cached = &metadataSidecars{index: photos.NewSidecarIndex(), captures: map[string]photos.Capture{}, errors: map[string]error{}}
		for _, candidate := range r.directories[dir] {
			file := r.files[candidate]
			if file.Size > 4<<20 {
				continue
			}
			raw, err := r.read(ctx, file, 4<<20)
			if err != nil {
				if ctx.Err() != nil {
					return photos.Capture{}, ctx.Err()
				}
				continue
			}
			addErr := cached.index.Add(candidate, raw)
			capture, parseErr := photos.ParseTakeoutSidecar(raw)
			clear(raw)
			if addErr == nil {
				cached.captures[candidate], cached.errors[candidate] = capture, parseErr
			}
		}
		if len(r.cache) >= 16 {
			r.cache = map[string]*metadataSidecars{}
		}
		r.cache[dir] = cached
	}
	matched, err := cached.index.Match(base)
	if err != nil {
		return photos.Capture{}, err
	}
	return cached.captures[matched], cached.errors[matched]
}

func (r *metadataResolver) readSidecar(ctx context.Context, name, title string) (photos.Capture, error) {
	file := r.files[name]
	if file.Size > 4<<20 {
		return photos.Capture{}, errors.New("sidecar exceeds size limit")
	}
	raw, err := r.read(ctx, file, 4<<20)
	if err != nil {
		return photos.Capture{}, err
	}
	defer clear(raw)
	var doc struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return photos.Capture{}, err
	}
	if doc.Title != "" && doc.Title != title {
		return photos.Capture{}, photos.ErrAmbiguousSidecar
	}
	return photos.ParseTakeoutSidecar(raw)
}

func (r *metadataResolver) resolve(ctx context.Context, file catalog.File, sidecarsOnly bool) (photos.MetadataResult, error) {
	result := photos.MetadataResult{}
	select {
	case thumbnailBackfill <- struct{}{}:
		defer func() { <-thumbnailBackfill }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	if file.CaptureUserCorrected {
		return result, nil
	}
	var takeout, embedded, client *photos.Capture
	var sourceErr error
	capture, err := r.sidecar(ctx, file.Path)
	if err == nil {
		takeout = &capture
	} else if !errors.Is(err, photos.ErrNoCaptureMetadata) {
		sourceErr = err
	}
	if takeout == nil && file.CaptureTime != nil && strings.HasPrefix(file.CaptureSource, "takeout-") {
		takeout = &photos.Capture{Time: *file.CaptureTime, OffsetMinutes: file.CaptureOffsetMinutes, Source: file.CaptureSource}
	}
	if takeout == nil && !sidecarsOnly && (strings.EqualFold(path.Ext(file.Path), ".jpg") || strings.EqualFold(path.Ext(file.Path), ".jpeg")) {
		raw, readErr := r.read(ctx, file, 4<<20)
		if readErr != nil {
			return result, readErr
		}
		capture, parseErr := photos.ParseEmbedded(file.Path, raw)
		clear(raw)
		if parseErr == nil {
			embedded = &capture
		} else if !errors.Is(parseErr, photos.ErrNoCaptureMetadata) && sourceErr == nil {
			sourceErr = parseErr
		}
	}
	if file.CaptureTime != nil {
		client = &photos.Capture{Time: *file.CaptureTime, OffsetMinutes: file.CaptureOffsetMinutes, Source: file.CaptureSource}
		if strings.HasPrefix(file.CaptureSource, "takeout-") && takeout == nil {
			takeout = client
			client = nil
		}
		if strings.HasPrefix(file.CaptureSource, "exif-") && embedded == nil {
			embedded = client
			client = nil
		}
		// Preserve a previously resolved source when no new source exists.
		if takeout == nil && embedded == nil {
			result.Capture = client
			return result, sourceErr
		}
	}
	if capture, ok := photos.Resolve(photos.Candidates{Takeout: takeout, Embedded: embedded, Client: client}); ok {
		result.Capture = &capture
	}
	return result, sourceErr
}
