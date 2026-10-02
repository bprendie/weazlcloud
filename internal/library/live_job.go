package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type LivePhotoEntry struct {
	ID         string `json:"id"`
	Hash       string `json:"hash"`
	Path       string `json:"path"`
	Identifier string `json:"identifier,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Status     string `json:"status"`
	Category   string `json:"category,omitempty"`
}
type LivePhotoJob struct {
	Status    string           `json:"status"`
	DryRun    bool             `json:"dry_run"`
	Total     int              `json:"total"`
	Examined  int              `json:"examined"`
	Paired    int              `json:"paired"`
	Pairable  int              `json:"pairable"`
	Ambiguous int              `json:"ambiguous"`
	Orphan    int              `json:"orphan"`
	Failed    int              `json:"failed"`
	Entries   []LivePhotoEntry `json:"entries,omitempty"`
}

func (l *Library) liveJobPath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-live-photos.enc")
}
func (l *Library) loadLiveJobLocked() error {
	if l.liveJob != nil {
		return nil
	}
	f, err := os.Open(l.liveJobPath())
	if os.IsNotExist(err) {
		l.liveJob = &LivePhotoJob{Status: "idle"}
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 {
		return errors.New("invalid Live Photo checkpoint")
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return err
	}
	defer clear(plain)
	var job LivePhotoJob
	if json.Unmarshal(plain, &job) != nil || len(job.Entries) > 200000 {
		return errors.New("invalid Live Photo checkpoint")
	}
	l.liveJob = &job
	return nil
}
func (l *Library) saveLiveJobLocked() error {
	raw, err := json.Marshal(l.liveJob)
	if err != nil {
		return err
	}
	defer clear(raw)
	if len(raw) > 64<<20 {
		return errors.New("Live Photo checkpoint exceeds limit")
	}
	wrapped, err := l.vault.Wrap(raw)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(l.liveJobPath(), wrapped, 0600)
}
func (l *Library) LivePhotoStatus(report bool) (LivePhotoJob, error) {
	if !l.vault.Unlocked() {
		return LivePhotoJob{}, vault.ErrLocked
	}
	l.liveMu.Lock()
	defer l.liveMu.Unlock()
	if err := l.loadLiveJobLocked(); err != nil {
		return LivePhotoJob{}, err
	}
	job := *l.liveJob
	job.Entries = nil
	if report {
		job.Entries = append([]LivePhotoEntry(nil), l.liveJob.Entries[:min(100, len(l.liveJob.Entries))]...)
	}
	return job, nil
}
func liveCandidate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".heic", ".heif", ".jpg", ".jpeg", ".mov", ".mp4":
		return true
	}
	return false
}
func (l *Library) SetLivePhotoJob(ctx context.Context, action string) (LivePhotoJob, error) {
	if action != "start" && action != "dry-run" && action != "resume" && action != "pause" && action != "retry" {
		return LivePhotoJob{}, errors.New("invalid Live Photo action")
	}
	l.mu.Lock()
	err := l.ensurePhotoIndexLocked(ctx)
	if err != nil {
		l.mu.Unlock()
		return LivePhotoJob{}, err
	}
	l.photoMu.Lock()
	files := append([]LivePhotoEntry(nil), []LivePhotoEntry{}...)
	for _, f := range l.photoRows {
		if f.Present && !f.Folder && liveCandidate(f.Path) && f.PhotoParentID == "" && len(f.PhotoComponents) < 2 {
			files = append(files, LivePhotoEntry{ID: f.EntryID, Hash: f.Hash, Path: f.Path, Status: "pending"})
		}
	}
	l.photoMu.Unlock()
	l.mu.Unlock()
	l.liveMu.Lock()
	if err := l.loadLiveJobLocked(); err != nil {
		l.liveMu.Unlock()
		return LivePhotoJob{}, err
	}
	if action == "pause" {
		if l.liveCancel != nil {
			l.liveCancel()
		}
		l.liveJob.Status = "paused"
	} else if l.liveRunning {
		l.liveMu.Unlock()
		return LivePhotoJob{}, errors.New("Live Photo repair already running")
	} else {
		if action == "start" || action == "dry-run" {
			previous := map[string]LivePhotoEntry{}
			for _, e := range l.liveJob.Entries {
				if e.Status == "examined" {
					previous[e.ID] = e
				}
			}
			for i, e := range files {
				if old, ok := previous[e.ID]; ok && old.Hash == e.Hash {
					files[i] = old
					files[i].Path = e.Path
				}
			}
			l.liveJob = &LivePhotoJob{Status: "queued", DryRun: action == "dry-run", Entries: files, Total: len(files)}
		} else {
			l.liveJob.Status = "queued"
			if action == "retry" {
				for i := range l.liveJob.Entries {
					if l.liveJob.Entries[i].Status == "failed" {
						l.liveJob.Entries[i].Status = "pending"
					}
				}
			}
		}
	}
	err = l.saveLiveJobLocked()
	job := *l.liveJob
	job.Entries = nil
	l.liveMu.Unlock()
	if err == nil && action != "pause" {
		l.startLiveWorker()
	}
	return job, err
}
func (l *Library) ResumeLivePhotos() {
	if !l.vault.Unlocked() {
		return
	}
	l.liveMu.Lock()
	err := l.loadLiveJobLocked()
	resume := err == nil && (l.liveJob.Status == "running" || l.liveJob.Status == "queued")
	if resume {
		l.liveJob.Status = "queued"
	}
	l.liveMu.Unlock()
	if resume {
		l.startLiveWorker()
	}
}
func (l *Library) startLiveWorker() {
	ctx, release := l.previewContext(context.Background())
	l.liveMu.Lock()
	if l.liveRunning || l.liveJob == nil || l.liveJob.Status != "queued" || ctx.Err() != nil {
		l.liveMu.Unlock()
		release()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	l.liveCancel = cancel
	l.liveRunning = true
	l.liveJob.Status = "running"
	if l.saveLiveJobLocked() != nil {
		l.liveRunning = false
		l.liveJob.Status = "paused_error"
		l.liveMu.Unlock()
		cancel()
		release()
		return
	}
	l.liveMu.Unlock()
	go func() {
		defer release()
		defer cancel()
		defer func() {
			l.liveMu.Lock()
			l.liveRunning = false
			if l.liveJob.Status == "running" {
				l.liveJob.Status = "queued"
				_ = l.saveLiveJobLocked()
			}
			restart := ctx.Err() == nil && l.liveJob.Status == "queued"
			l.liveMu.Unlock()
			if restart {
				l.startLiveWorker()
			}
		}()
		l.runLiveWorker(ctx)
	}()
}
