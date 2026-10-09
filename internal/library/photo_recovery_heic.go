package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

// RecoverHEICPreviews requeues legacy environmental rejections once, preserving
// good derivatives and explicit pause. Originals and canonical metadata are untouched.
func (l *Library) RecoverHEICPreviews(ctx context.Context) (int, error) {
	return l.recoverPreviewFailures(ctx, "heic-scratch-v1", ".weazl-heic-scratch-recovery-v1.enc", func(f catalog.File) bool {
		ext := strings.ToLower(filepath.Ext(f.Path))
		return ext == ".heic" || ext == ".heif"
	})
}

// RecoverExpandedPreviews retries old pixel/transport bounds and MOV decoder
// failures once. Good derivatives and the user's explicit pause are preserved.
func (l *Library) RecoverExpandedPreviews(ctx context.Context) (int, error) {
	return l.recoverPreviewFailures(ctx, "preview-bounds-v1", ".weazl-preview-bounds-recovery-v1.enc", func(f catalog.File) bool {
		ext := strings.ToLower(filepath.Ext(f.Path))
		kind := photoPreviewKind(f.Path)
		return f.Size > 0 && f.Size <= int64(previewrpc.InputLimit(kind)) && (kind == "video" || ext == ".jpg" || ext == ".jpeg")
	})
}

func (l *Library) recoverPreviewFailures(ctx context.Context, version, filename string, eligible func(catalog.File) bool) (int, error) {
	if socket := os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET"); socket != "" {
		if err := previewrpc.Ready(socket); err != nil {
			return 0, err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return 0, err
	}
	marker := filepath.Join(filepath.Dir(l.repo), filename)
	if raw, err := os.ReadFile(marker); err == nil {
		plain, err := l.vault.Unwrap(raw)
		defer clear(plain)
		if err != nil {
			return 0, err
		}
		if string(plain) == version {
			return 0, nil
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	l.photoMu.Lock()
	byID := make(map[string]uint64)
	for _, f := range l.photoRows {
		if eligible(f) {
			byID[f.EntryID] = f.Revision
		}
	}
	l.photoMu.Unlock()
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return 0, err
	}
	recovered := 0
	for _, job := range l.photoJobs.Jobs {
		// Capture-date repair can advance revision without changing pixels.
		if byID[job.AssetID] == 0 || job.Status != photos.JobFailed {
			continue
		}
		if job.ErrorCategory != "invalid_or_unsupported_media" && job.ErrorCategory != "previous_terminal_failure" && job.ErrorCategory != "worker_environment" && job.ErrorCategory != "unsupported_size" {
			continue
		}
		l.photoMu.Lock()
		f, ok := l.photoByID[job.AssetID]
		l.photoMu.Unlock()
		if !ok {
			continue
		}
		key, err := thumbnailKey(l.vault, f, 320)
		if err != nil {
			return recovered, err
		}
		if err := l.clearPhotoFailure(key); err != nil {
			return recovered, err
		}
		job.Status, job.Attempts, job.Progress, job.ErrorCategory = photos.JobPending, 0, 0, ""
		job.NextAttemptAt = time.Time{}
		l.photoJobs.Replace(job)
		recovered++
	}
	if err := l.savePhotoJobsLocked(); err != nil {
		return recovered, err
	}
	wrapped, err := l.vault.Wrap([]byte(version))
	if err == nil {
		err = cryptox.AtomicWrite(marker, wrapped, 0600)
	}
	return recovered, err
}

type PhotoFailureItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Category  string `json:"category"`
	Retryable bool   `json:"retryable"`
}
type PhotoFailureReport struct {
	Items   []PhotoFailureItem `json:"items"`
	Total   int                `json:"total"`
	Next    int                `json:"next"`
	Counts  map[string]int     `json:"counts"`
	Formats map[string]int     `json:"formats"`
}

func (l *Library) PhotoFailures(ctx context.Context, start int) (PhotoFailureReport, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	report := PhotoFailureReport{Items: []PhotoFailureItem{}, Counts: map[string]int{}, Formats: map[string]int{}, Next: -1}
	if start < 0 {
		return report, errors.New("invalid report offset")
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return report, err
	}
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		return report, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	for _, job := range l.photoJobs.Jobs {
		if job.Status != photos.JobFailed {
			continue
		}
		file, ok := l.photoByID[job.AssetID]
		if !ok || !file.Present || job.Revision != file.Revision {
			continue
		}
		index := report.Total
		report.Total++
		report.Counts[job.ErrorCategory]++
		report.Formats[strings.ToLower(filepath.Ext(file.Path))]++
		if index < start || len(report.Items) >= 100 {
			continue
		}
		report.Items = append(report.Items, PhotoFailureItem{ID: file.EntryID, Path: file.Path, Category: job.ErrorCategory, Retryable: strings.HasPrefix(job.ErrorCategory, "worker_")})
	}
	if start+len(report.Items) < report.Total {
		report.Next = start + len(report.Items)
	}
	return report, nil
}
