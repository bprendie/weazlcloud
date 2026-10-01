package library

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

type PhotoMetadataOptions struct {
	Root           string
	CheckpointPath string
	DryRun         bool
	SidecarsOnly   bool
}

type PhotoMetadataReport struct {
	Examined   int
	Updated    int
	Skipped    int
	Unresolved int
	Errors     int
}

func (l *Library) BackfillPhotoMetadata(ctx context.Context, options PhotoMetadataOptions) (PhotoMetadataReport, error) {
	files, err := l.photoMetadataFiles(ctx, options.Root)
	if err != nil {
		return PhotoMetadataReport{}, err
	}
	assets := make([]photos.Asset, 0, len(files))
	byID := make(map[string]catalog.File, len(files))
	for _, file := range files {
		asset := photos.Asset{ID: file.EntryID, Revision: file.Revision, Path: file.Path}
		assets = append(assets, asset)
		byID[asset.ID] = file
	}
	report, err := photos.RunMetadataMigration(ctx, assets,
		photos.MigrationOptions{CheckpointPath: options.CheckpointPath, DryRun: options.DryRun},
		func(resolveCtx context.Context, asset photos.Asset) (photos.MetadataResult, error) {
			file, ok := byID[asset.ID]
			if !ok {
				return photos.MetadataResult{}, errLibraryFileMissing
			}
			result := photos.MetadataResult{}
			var original []byte
			if !options.SidecarsOnly {
				var readErr error
				original, readErr = l.readPhotoPrefix(resolveCtx, file)
				if readErr != nil {
					return photos.MetadataResult{}, readErr
				}
			}
			if !file.CaptureUserCorrected {
				var takeout *photos.Capture
				if raw, readErr := l.readSmallPhotoFile(resolveCtx, file.Path+".json"); readErr == nil {
					capture, parseErr := photos.ParseTakeoutSidecar(raw)
					if parseErr == nil {
						takeout = &capture
					} else if !errors.Is(parseErr, photos.ErrNoCaptureMetadata) {
						return photos.MetadataResult{}, parseErr
					}
				} else if !errors.Is(readErr, errLibraryFileMissing) {
					return photos.MetadataResult{}, readErr
				}
				var embedded *photos.Capture
				if !options.SidecarsOnly {
					capture, parseErr := photos.ParseEmbedded(file.Path, original)
					if parseErr == nil {
						embedded = &capture
					} else if !errors.Is(parseErr, photos.ErrNoCaptureMetadata) {
						return photos.MetadataResult{}, parseErr
					}
				}
				capture, found := photos.Resolve(photos.Candidates{Takeout: takeout, Embedded: embedded})
				if found {
					result.Capture = &capture
				}
			}
			if !options.SidecarsOnly {
				media, parseErr := photos.ParseMediaMetadata(file.Path, original)
				if parseErr == nil {
					result.Media = &media
				} else if !errors.Is(parseErr, photos.ErrNoCaptureMetadata) {
					return photos.MetadataResult{}, parseErr
				}
			}
			return result, nil
		},
		func(asset photos.Asset, result photos.MetadataResult) error {
			file := byID[asset.ID]
			return l.applyPhotoMetadata(ctx, file, result)
		})
	return PhotoMetadataReport{Examined: report.Examined, Updated: report.Updated, Skipped: report.Skipped, Unresolved: report.Unresolved, Errors: report.Errors}, err
}

var errLibraryFileMissing = errors.New("photo metadata file is missing")

func (l *Library) photoMetadataFiles(ctx context.Context, root string) ([]catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(root), "/")
	files := make([]catalog.File, 0)
	for _, file := range l.catalog.List() {
		if file.Folder || !photoMedia(file.Path) || file.PhotoParentID != "" || !inPhotoRoot(file.Path) {
			continue
		}
		if prefix != "" && file.Path != prefix && !strings.HasPrefix(file.Path, prefix+"/") {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

func (l *Library) readSmallPhotoFile(ctx context.Context, name string) ([]byte, error) {
	name, err := cleanPath(name)
	if err != nil {
		return nil, errLibraryFileMissing
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	file, ok := l.catalog.Get(name)
	if !ok || file.Folder || file.Size > 4<<20 {
		return nil, errLibraryFileMissing
	}
	ref, err := l.capture(file)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := l.readReference(ctx, ref, &body); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func (l *Library) readPhotoPrefix(ctx context.Context, file catalog.File) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	current, ok := l.catalog.Get(file.Path)
	if !ok {
		return nil, errLibraryFileMissing
	}
	ref, err := l.capture(current)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	length := current.Size
	if length > 4<<20 {
		length = 4 << 20
	}
	if length == 0 {
		return nil, photos.ErrNoCaptureMetadata
	}
	if err := l.readReferenceRange(ctx, ref, 0, length, &body); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func (l *Library) applyPhotoMetadata(ctx context.Context, file catalog.File, result photos.MetadataResult) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	current, ok := l.catalog.Get(file.Path)
	if !ok {
		return errLibraryFileMissing
	}
	var capture *catalog.CaptureMetadata
	if result.Capture != nil {
		capture = &catalog.CaptureMetadata{
			Time: captureTime(*result.Capture), OffsetMinutes: result.Capture.OffsetMinutes,
			Source: result.Capture.Source, UserCorrected: result.Capture.UserCorrected,
		}
	}
	var media *catalog.MediaMetadata
	if result.Media != nil {
		media = &catalog.MediaMetadata{Width: result.Media.Width, Height: result.Media.Height, DurationMillis: result.Media.DurationMillis, Orientation: result.Media.Orientation, Camera: result.Media.Camera}
	}
	updated, err := l.catalog.UpdatePhotoMetadata(current.EntryID, current.Revision, capture, media)
	if err != nil {
		return err
	}
	if updated.Revision != current.Revision {
		l.publishChange(Change{Kind: "photo-metadata", Paths: []string{updated.Path}})
	}
	return nil
}

func (l *Library) SetPhotoCapture(ctx context.Context, name string, metadata catalog.CaptureMetadata) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil {
		return catalog.File{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	current, ok := l.catalog.Get(name)
	if !ok || current.Folder || !photoMedia(current.Path) {
		return catalog.File{}, errLibraryFileMissing
	}
	updated, err := l.catalog.UpdateCapture(current.EntryID, current.Revision, metadata)
	if err != nil {
		return catalog.File{}, err
	}
	if updated.Revision != current.Revision {
		l.publishChange(Change{Kind: "photo-metadata", Paths: []string{updated.Path}})
	}
	return updated, nil
}

func captureTime(capture photos.Capture) *time.Time {
	if capture.Time.IsZero() {
		return nil
	}
	value := capture.Time
	return &value
}
