package library

import (
	"errors"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

// Shares the durable ingestion outbox: receipt acknowledgement happens only
// after both metadata and derivative work have been recorded.
func (l *Library) queueMetadataIngest(files []catalog.File, force ...bool) error {
	if l.photoAutoDisabled {
		return nil
	}
	l.metadataMu.Lock()
	if err := l.loadMetadataLocked(); err != nil {
		l.metadataMu.Unlock()
		return err
	}
	previous := cloneMetadataJob(l.metadataJob)
	if l.metadataJob != nil && l.metadataJob.Options.DryRun && !strings.HasPrefix(l.metadataJob.Status, "complete") {
		l.metadataMu.Unlock()
		return errors.New("metadata dry run is active; ingestion will retry")
	}
	if l.metadataJob == nil || l.metadataJob.Options.DryRun {
		l.metadataJob = &PhotoMetadataJob{Version: 1, Parser: "capture-v2", Options: PhotoMetadataOptionsJob{Root: "Photos"}, Status: "queued"}
	}
	if l.metadataJob.Options.Root != "Photos" {
		if l.metadataRunning || !strings.HasPrefix(l.metadataJob.Status, "complete") {
			l.metadataMu.Unlock()
			return errors.New("scoped metadata repair is active; ingestion will retry")
		}
		l.metadataJob = &PhotoMetadataJob{Version: 1, Parser: "capture-v2", Options: PhotoMetadataOptionsJob{Root: "Photos"}, Status: "queued"}
	}
	job := l.metadataJob
	job.Sequence = max(job.Sequence, metadataSequence(previous))
	positions := map[string]int{}
	for i, entry := range job.Entries {
		positions[entry.ID] = i
	}
	for _, file := range files {
		if !inPhotoRoot(file.Path) || file.Folder || file.PhotoParentID != "" || !photoMedia(file.Path) {
			continue
		}
		entry := PhotoMetadataEntry{ID: file.EntryID, Revision: file.Revision, Hash: file.Hash, Path: file.Path, Status: "pending"}
		if i, ok := positions[file.EntryID]; ok {
			old := job.Entries[i]
			if len(force) == 0 && old.Hash == file.Hash && old.Path == file.Path && (old.Status == "updated" || old.Status == "unchanged" || old.Status == "pending") {
				continue
			}
			entry.QueueVersion = old.QueueVersion + 1
			job.Entries[i] = entry
		} else {
			if len(job.Entries) >= 200000 {
				l.metadataJob = previous
				l.metadataMu.Unlock()
				return errors.New("metadata ingestion reached capacity")
			}
			job.Entries = append(job.Entries, entry)
			if file.CaptureTime != nil {
				job.InitialKnown++
			} else {
				job.InitialUnknown++
			}
			positions[entry.ID] = len(job.Entries) - 1
		}
	}
	job.Total = len(job.Entries)
	l.countMetadataLocked()
	if job.Status != "paused" && job.Status != "paused_error" {
		job.Status = "queued"
	}
	err := l.saveMetadataLocked()
	if err != nil {
		l.metadataJob = previous
	}
	l.metadataMu.Unlock()
	if err == nil {
		l.startMetadataWorker()
	}
	return err
}
