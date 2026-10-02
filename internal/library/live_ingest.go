package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"time"
)

// Merge late ZIP/import arrivals into the encrypted queue, including while the
// scanner runs. An explicit pause and dry-run never become automatic apply.
func (l *Library) queueLiveIngest(arrivals []catalog.File) error {
	ctx, release := l.previewContext(context.Background())
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files := []LivePhotoEntry{}
	for _, f := range arrivals {
		if f.Present && !f.Folder && liveCandidate(f.Path) && f.PhotoParentID == "" && len(f.PhotoComponents) < 2 {
			files = append(files, LivePhotoEntry{ID: f.EntryID, Hash: f.Hash, Path: f.Path, Status: "pending"})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.liveMu.Lock()
	if err := l.loadLiveJobLocked(); err != nil {
		l.liveMu.Unlock()
		return err
	}
	if l.liveJob.DryRun || l.liveJob.Status == "idle" || l.liveJob.Status == "paused" || l.liveJob.Status == "paused_error" {
		l.liveMu.Unlock()
		return nil
	}
	positions := map[string]int{}
	for i, e := range l.liveJob.Entries {
		positions[e.ID] = i
	}
	changed := false
	for _, e := range files {
		if i, ok := positions[e.ID]; ok {
			if l.liveJob.Entries[i].Hash != e.Hash {
				l.liveJob.Entries[i] = e
				changed = true
			}
		} else if len(l.liveJob.Entries) < 200000 {
			l.liveJob.Entries = append(l.liveJob.Entries, e)
			positions[e.ID] = len(l.liveJob.Entries) - 1
			changed = true
		} else {
			l.liveMu.Unlock()
			return errors.New("Live Photo queue capacity reached")
		}
	}
	if !changed {
		l.liveMu.Unlock()
		return nil
	}
	l.liveJob.Total = len(l.liveJob.Entries)
	start := !l.liveRunning
	if start || l.liveJob.Status == "complete" || l.liveJob.Status == "complete_with_issues" {
		l.liveJob.Status = "queued"
	}
	err := l.saveLiveJobLocked()
	l.liveMu.Unlock()
	if err == nil && start {
		l.startLiveWorker()
	}
	return err
}
