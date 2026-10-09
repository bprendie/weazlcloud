package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os/exec"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func TestHighResolutionJPEGPreview(t *testing.T) {
	var source bytes.Buffer
	// Reproduce the 100-MP camera originals rejected by the former 32-MP ceiling.
	if err := jpeg.Encode(&source, image.NewGray(image.Rect(0, 0, 11656, 8742)), nil); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"go", "turbo"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "turbo" {
				if _, err := exec.LookPath(turboHelper); err != nil {
					t.Skip("native helper absent")
				}
			}
			t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", mode)
			l := newPhotoIndexTestLibrary(t)
			f, err := l.Put(context.Background(), "Photos/high-resolution.jpg", source.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			f, err = l.Metadata(context.Background(), f.Path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := l.thumbnailFor(context.Background(), f, 320, true); err != nil {
				t.Fatal(err)
			}
			for _, size := range []int{320, 1280} {
				body, mime, err := l.Thumbnail(context.Background(), f.Path, size)
				cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(body))
				if err != nil || decodeErr != nil || mime != "image/jpeg" || cfg.Width != size || cfg.Height != size*8742/11656 {
					t.Fatal(cfg, mime, err, decodeErr)
				}
			}
		})
	}
	if validThumbnailConfig(image.Config{Width: 16000, Height: 12000}) {
		t.Fatal("pixel ceiling removed")
	}
}

func TestPreviewBoundsRecoveryIsScopedAndOnce(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	var files []catalog.File
	for _, name := range []string{"Photos/high.jpg", "Photos/movie.mov", "Photos/broken.png", "Photos/ready.jpg"} {
		f, err := l.Put(ctx, name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
		f, _ = l.catalog.Get(f.Path)
		files = append(files, f)
	}
	if _, err := l.PhotoPage(ctx, 20, "", ""); err != nil {
		t.Fatal(err)
	}
	l.photoJobsMu.Lock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	for i, f := range files {
		job := photos.NewMediaJob(l.ownerID, f.EntryID, f.Revision, photoJobOperation, thumbnailRenderer, 1)
		job.Status, job.ErrorCategory = photos.JobFailed, "invalid_or_unsupported_media"
		if i == 1 {
			job.ErrorCategory = "unsupported_size"
		}
		if i == 3 {
			job.Status = photos.JobSucceeded
		}
		l.photoJobs.Replace(job)
		if i != 3 {
			key, _ := thumbnailKey(l.vault, f, 320)
			if err := l.savePhotoFailure(key, errors.New("old bounds")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := l.savePhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	l.photoJobsMu.Unlock()
	key, _ := thumbnailKey(l.vault, files[3], 320)
	if err := l.writeThumbnailCache(key, thumbnailEnvelope{Body: []byte("existing"), ContentType: "image/jpeg", Size: 320}); err != nil {
		t.Fatal(err)
	}
	l.photoPrep.Paused = true
	recovered, err := l.RecoverExpandedPreviews(ctx)
	if err != nil || recovered != 2 || !l.photoPrep.Paused {
		t.Fatal(recovered, err)
	}
	if recovered, err = l.RecoverExpandedPreviews(ctx); err != nil || recovered != 0 {
		t.Fatal("replayed", recovered, err)
	}
	for i, f := range files[:3] {
		key, _ := thumbnailKey(l.vault, f, 320)
		err := l.photoFailure(key)
		if i < 2 && err != nil || i == 2 && !errors.Is(err, ErrPhotoPreviouslyFailed) {
			t.Fatal("wrong failure cleared", i, err)
		}
	}
	if body, _, ok := l.readThumbnailCache(key); !ok || string(body) != "existing" {
		t.Fatal("working preview discarded")
	}
}
