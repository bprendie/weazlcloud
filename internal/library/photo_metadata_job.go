package library

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var ErrMetadataAction = errors.New("invalid metadata job action or options")
var ErrMetadataBusy = errors.New("metadata job already active with different options")

type PhotoMetadataOptionsJob struct {
	Root         string `json:"root,omitempty"`
	DryRun       bool   `json:"dry_run"`
	SidecarsOnly bool   `json:"sidecars_only"`
}
type PhotoMetadataEntry struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	Revision     uint64 `json:"revision"`
	QueueVersion uint64 `json:"queue_version,omitempty"`
	Attempts     int    `json:"attempts,omitempty"`
	Status       string `json:"status"`
	Category     string `json:"category,omitempty"`
	Source       string `json:"source,omitempty"`
}
type PhotoMetadataJob struct {
	Sequence          uint64                  `json:"sequence"`
	Version           int                     `json:"version"`
	Parser            string                  `json:"parser"`
	Options           PhotoMetadataOptionsJob `json:"options"`
	Status            string                  `json:"status"`
	Total             int                     `json:"total"`
	Examined          int                     `json:"examined"`
	Updated           int                     `json:"updated"`
	Unchanged         int                     `json:"unchanged"`
	Unresolved        int                     `json:"unresolved"`
	Failed            int                     `json:"failed"`
	InitialKnown      int                     `json:"initial_known"`
	InitialUnknown    int                     `json:"initial_unknown"`
	SupportedEmbedded []string                `json:"supported_embedded"`
	Error             string                  `json:"error,omitempty"`
	UpdatedAt         time.Time               `json:"updated_at"`
	Entries           []PhotoMetadataEntry    `json:"-"`
}
type metadataJobDisk struct {
	Job     PhotoMetadataJob     `json:"job"`
	Entries []PhotoMetadataEntry `json:"entries"`
}

func (l *Library) metadataJobPath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-metadata.enc")
}
func (l *Library) loadMetadataLocked() error {
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if l.metadataJob != nil {
		return nil
	}
	var selected *PhotoMetadataJob
	for _, name := range []string{l.metadataJobPath(), l.metadataDryPath()} {
		job, err := l.readMetadataCheckpoint(name)
		if err != nil {
			return err
		}
		if job != nil && (selected == nil || job.Sequence > selected.Sequence || job.Sequence == selected.Sequence && job.UpdatedAt.After(selected.UpdatedAt)) {
			selected = job
		}
	}
	if selected != nil && selected.Status == "running" {
		selected.Status = "queued"
	}
	l.metadataJob = selected
	return nil
}
func (l *Library) saveMetadataLocked() (err error) {
	job := l.metadataJob
	if job.Sequence == ^uint64(0) {
		return errors.New("metadata checkpoint sequence overflow")
	}
	previousSequence := job.Sequence
	job.Sequence++
	defer func() {
		if err != nil {
			job.Sequence = previousSequence
		}
	}()
	l.metadataJob.UpdatedAt = time.Now().UTC()
	plain, err := json.Marshal(metadataJobDisk{Job: *l.metadataJob, Entries: l.metadataJob.Entries})
	if err != nil {
		return err
	}
	defer clear(plain)
	raw, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if len(raw) > 64<<20 {
		return errors.New("metadata job exceeds size limit")
	}
	name := l.metadataJobPath()
	if l.metadataJob.Options.DryRun {
		name = l.metadataDryPath()
	}
	return cryptox.AtomicWrite(name, raw, 0600)
}
func (l *Library) PhotoMetadataStatus() (PhotoMetadataJob, error) {
	l.metadataMu.Lock()
	if err := l.loadMetadataLocked(); err != nil {
		l.metadataMu.Unlock()
		return PhotoMetadataJob{}, err
	}
	state := PhotoMetadataJob{Status: "idle"}
	if l.metadataJob != nil {
		state = *l.metadataJob
		state.Entries = nil
	}
	state.SupportedEmbedded = []string{"jpeg-exif"}
	restart := state.Status == "queued" && !l.metadataRunning
	l.metadataMu.Unlock()
	if restart {
		l.startMetadataWorker()
	}
	return state, nil
}
func (l *Library) PhotoMetadataReport(cursor, limit int) ([]PhotoMetadataEntry, error) {
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if err := l.loadMetadataLocked(); err != nil {
		return nil, err
	}
	if cursor < 0 || limit < 1 || limit > 200 {
		return nil, ErrMetadataAction
	}
	if l.metadataJob == nil {
		return []PhotoMetadataEntry{}, nil
	}
	start := min(cursor, len(l.metadataJob.Entries))
	end := min(start+limit, len(l.metadataJob.Entries))
	return append([]PhotoMetadataEntry{}, l.metadataJob.Entries[start:end]...), nil
}
func (l *Library) SetPhotoMetadataJob(ctx context.Context, action string, options PhotoMetadataOptionsJob) (PhotoMetadataJob, error) {
	l.metadataMu.Lock()
	finishing := l.metadataRunning && l.metadataJob != nil && strings.HasPrefix(l.metadataJob.Status, "complete")
	done := l.metadataDone
	l.metadataMu.Unlock()
	if finishing {
		select {
		case <-done:
		case <-ctx.Done():
			return PhotoMetadataJob{}, ctx.Err()
		}
	}

	if action == "start" || action == "dry-run" {
		if options.Root == "" {
			options.Root = "Photos"
		}
		clean, err := cleanPath(options.Root)
		if err != nil || !inPhotoRoot(clean) {
			return PhotoMetadataJob{}, ErrMetadataAction
		}
		options.Root = clean
		options.DryRun = action == "dry-run" || options.DryRun
		files, err := l.photoMetadataFiles(ctx, options.Root)
		if err != nil {
			return PhotoMetadataJob{}, err
		}
		if len(files) > 200000 {
			return PhotoMetadataJob{}, errors.New("metadata job exceeds asset limit")
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		l.metadataMu.Lock()
		if err := l.loadMetadataLocked(); err != nil {
			l.metadataMu.Unlock()
			return PhotoMetadataJob{}, err
		}
		if !l.metadataRunning && (l.metadataJob == nil || strings.HasPrefix(l.metadataJob.Status, "complete")) {
			job := &PhotoMetadataJob{Version: 1, Parser: "capture-v2", Options: options, Status: "queued", Total: len(files)}
			job.Sequence = metadataSequence(l.metadataJob)
			job.SupportedEmbedded = []string{"jpeg-exif"}
			for _, file := range files {
				if file.CaptureTime != nil {
					job.InitialKnown++
				} else {
					job.InitialUnknown++
				}
				job.Entries = append(job.Entries, PhotoMetadataEntry{ID: file.EntryID, Path: file.Path, Hash: file.Hash, Revision: file.Revision, Status: "pending"})
			}
			previous := l.metadataJob
			l.metadataJob = job
			if err := l.saveMetadataLocked(); err != nil {
				l.metadataJob = previous
				l.metadataMu.Unlock()
				return PhotoMetadataJob{}, err
			}
		} else if l.metadataJob.Options != options {
			l.metadataMu.Unlock()
			return PhotoMetadataJob{}, ErrMetadataBusy
		}
		l.metadataMu.Unlock()
	} else {
		l.metadataMu.Lock()
		if err := l.loadMetadataLocked(); err != nil {
			l.metadataMu.Unlock()
			return PhotoMetadataJob{}, err
		}
		if l.metadataJob == nil {
			l.metadataMu.Unlock()
			return PhotoMetadataJob{}, ErrMetadataAction
		}
		previous := cloneMetadataJob(l.metadataJob)
		switch action {
		case "pause":
			l.metadataJob.Status = "paused"
			if l.metadataCancel != nil {
				l.metadataCancel()
			}
		case "resume", "retry":
			l.metadataJob.Status = "queued"
			l.metadataJob.Error = ""
			if action == "retry" {
				for i := range l.metadataJob.Entries {
					entry := &l.metadataJob.Entries[i]
					if entry.Status == "failed" || entry.Status == "unresolved" {
						entry.Status = "pending"
						entry.Category = ""
						entry.Attempts = 0
					}
				}
			}
		default:
			l.metadataMu.Unlock()
			return PhotoMetadataJob{}, ErrMetadataAction
		}
		l.countMetadataLocked()
		err := l.saveMetadataLocked()
		if err != nil && action != "pause" {
			l.metadataJob = previous
		}
		l.metadataMu.Unlock()
		if err != nil {
			return PhotoMetadataJob{}, err
		}
	}
	if action != "pause" {
		l.startMetadataWorker()
	}
	return l.PhotoMetadataStatus()
}
func (l *Library) clearMetadataMemory() {
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if l.metadataCancel != nil {
		l.metadataCancel()
	}
	if !l.metadataRunning {
		l.metadataJob = nil
	}
}
