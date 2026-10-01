package library

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

type metadataResolved struct {
	entry    PhotoMetadataEntry
	mutation *catalog.PhotoMetadataMutation
}

func (l *Library) startMetadataWorker() {
	l.metadataMu.Lock()
	if l.metadataRunning || l.metadataJob == nil || l.metadataJob.Status != "queued" || !l.vault.Unlocked() {
		l.metadataMu.Unlock()
		return
	}
	ctx, release := l.previewContext(context.Background())
	if ctx.Err() != nil {
		release()
		l.metadataMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	l.metadataCancel = cancel
	l.metadataRunning = true
	l.metadataDone = make(chan struct{})
	l.metadataJob.Status = "running"
	if err := l.saveMetadataLocked(); err != nil {
		l.metadataJob.Status = "paused_error"
		l.metadataJob.Error = "checkpoint_write_failed"
		l.metadataRunning = false
		cancel()
		release()
		close(l.metadataDone)
		l.metadataMu.Unlock()
		return
	}
	l.metadataMu.Unlock()
	go func() {
		defer release()
		defer cancel()
		defer func() {
			l.metadataMu.Lock()
			l.metadataRunning = false
			l.metadataCancel = nil
			close(l.metadataDone)
			restart := l.metadataJob != nil && l.metadataJob.Status == "queued" && l.vault.Unlocked()
			if !l.vault.Unlocked() {
				l.metadataJob = nil
			}
			l.metadataMu.Unlock()
			if restart {
				l.startMetadataWorker()
			}
		}()
		l.runMetadataWorker(ctx)
	}()
}
func (l *Library) runMetadataWorker(ctx context.Context) {
	resolver, err := l.newMetadataResolver(ctx)
	if err != nil {
		l.metadataWorkerError("catalog_unavailable", ctx)
		return
	}
	for ctx.Err() == nil {
		l.metadataMu.Lock()
		if l.metadataJob == nil || l.metadataJob.Status == "paused" {
			l.metadataMu.Unlock()
			return
		}
		options := l.metadataJob.Options
		pending := []PhotoMetadataEntry{}
		for _, entry := range l.metadataJob.Entries {
			if entry.Status == "pending" {
				pending = append(pending, entry)
				if len(pending) == 100 {
					break
				}
			}
		}
		l.metadataMu.Unlock()
		if err := resolver.refresh(ctx); err != nil {
			l.metadataWorkerError("catalog_unavailable", ctx)
			return
		}
		if len(pending) == 0 {
			l.metadataMu.Lock()
			if l.metadataJob.hasPending() {
				l.metadataMu.Unlock()
				continue
			}
			l.countMetadataLocked()
			l.metadataJob.Status = "complete"
			if l.metadataJob.Failed > 0 || l.metadataJob.Unresolved > 0 {
				l.metadataJob.Status = "complete_with_issues"
			}
			if err := l.saveMetadataLocked(); err != nil {
				l.metadataJob.Status = "paused_error"
				l.metadataJob.Error = "checkpoint_write_failed"
			}
			l.metadataMu.Unlock()
			return
		}
		resolved := make([]metadataResolved, 0, len(pending))
		for _, entry := range pending {
			if ctx.Err() != nil {
				break
			}
			workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			result := l.resolveMetadataEntry(workCtx, resolver, entry, options)
			cancel()
			resolved = append(resolved, result)
		}
		if ctx.Err() != nil {
			break
		}
		if err := l.commitMetadataResults(ctx, resolved, options.DryRun); err != nil {
			l.metadataWorkerError("durable_commit_failed", ctx)
			return
		}
	}
	l.metadataWorkerError("", ctx)
}
func (l *Library) resolveMetadataEntry(ctx context.Context, r *metadataResolver, entry PhotoMetadataEntry, options PhotoMetadataOptionsJob) metadataResolved {
	file, err := l.Metadata(ctx, entry.Path)
	if err != nil || file.EntryID != entry.ID || file.Hash != entry.Hash {
		item, detailErr := l.PhotoDetail(ctx, entry.ID)
		if detailErr != nil {
			item, detailErr = l.PhotoDetail(ctx, entry.ID, true)
		}
		if detailErr == nil {
			file, err = l.Metadata(ctx, item.Path)
		}
		root := options.Root
		if root == "" {
			root = "Photos"
		}
		if detailErr != nil || err != nil || file.EntryID != entry.ID || !(file.Path == root || strings.HasPrefix(file.Path, root+"/")) {
			entry.Status, entry.Category = "failed", "source_changed_or_missing"
			return metadataResolved{entry: entry}
		}
		entry.Path, entry.Hash, entry.Revision = file.Path, file.Hash, file.Revision
	}
	if file.CaptureUserCorrected {
		entry.Status, entry.Source = "unchanged", "user"
		return metadataResolved{entry: entry}
	}
	result, err := r.resolve(ctx, file, options.SidecarsOnly)
	if err != nil {
		entry.Status, entry.Category = "failed", "invalid_or_unreadable_metadata"
		if errors.Is(err, photos.ErrAmbiguousSidecar) {
			entry.Category = "ambiguous_sidecar"
		}
		return metadataResolved{entry: entry}
	}
	if result.Capture == nil {
		entry.Status = "unresolved"
		return metadataResolved{entry: entry}
	}
	capture := &catalog.CaptureMetadata{Time: captureTime(*result.Capture), OffsetMinutes: result.Capture.OffsetMinutes, Source: result.Capture.Source}
	entry.Status, entry.Source = "updated", capture.Source
	if file.CaptureTime != nil && file.CaptureTime.Equal(*capture.Time) && equalOffset(file.CaptureOffsetMinutes, capture.OffsetMinutes) && file.CaptureSource == capture.Source {
		entry.Status = "unchanged"
	}
	entry.Revision = file.Revision
	return metadataResolved{entry: entry, mutation: &catalog.PhotoMetadataMutation{ID: file.EntryID, Revision: file.Revision, Path: file.Path, Hash: file.Hash, Capture: capture}}
}
func equalOffset(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (l *Library) commitMetadataResults(ctx context.Context, results []metadataResolved, dry bool) error {
	if !dry {
		mutations := []catalog.PhotoMetadataMutation{}
		for _, result := range results {
			if result.mutation != nil {
				mutations = append(mutations, *result.mutation)
			}
		}
		var batch catalog.PhotoMetadataBatch
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = ctx.Err(); err != nil {
				return err
			}
			l.mu.Lock()
			err = l.ensure(ctx)
			if err == nil {
				batch, err = l.catalog.UpdatePhotoMetadataBatch(mutations)
			}
			if err == nil && len(batch.Changed) > 0 {
				paths := make([]string, 0, len(batch.Changed))
				for _, file := range batch.Changed {
					paths = append(paths, file.Path)
				}
				l.publishChange(Change{Kind: "photo-metadata", Paths: paths})
			}
			l.mu.Unlock()
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
		}
		if err != nil {
			return err
		}
		versions := map[string]uint64{}
		conflicts := map[string]bool{}
		for _, file := range batch.Files {
			versions[file.EntryID] = file.Revision
		}
		for _, id := range batch.Conflicts {
			conflicts[id] = true
		}
		for i := range results {
			if conflicts[results[i].entry.ID] {
				results[i].entry.Status, results[i].entry.Category = "failed", "concurrent_change"
				results[i].entry.Attempts++
				if results[i].entry.Attempts < 3 {
					results[i].entry.Status = "pending"
				}
			}
			if rev, ok := versions[results[i].entry.ID]; ok {
				results[i].entry.Revision = rev
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if l.metadataJob == nil {
		return context.Canceled
	}
	previous := cloneMetadataJob(l.metadataJob)
	outcomes := map[string]PhotoMetadataEntry{}
	for _, result := range results {
		outcomes[result.entry.ID] = result.entry
	}
	for i, entry := range l.metadataJob.Entries {
		if result, ok := outcomes[entry.ID]; ok && entry.QueueVersion == result.QueueVersion {
			l.metadataJob.Entries[i] = result
		}
	}
	l.countMetadataLocked()
	if err := l.saveMetadataLocked(); err != nil {
		l.metadataJob = previous
		return err
	}
	return nil
}
func (l *Library) countMetadataLocked() {
	job := l.metadataJob
	job.Examined, job.Updated, job.Unchanged, job.Unresolved, job.Failed = 0, 0, 0, 0, 0
	for _, entry := range job.Entries {
		switch entry.Status {
		case "updated":
			job.Updated++
		case "unchanged":
			job.Unchanged++
		case "unresolved":
			job.Unresolved++
		case "failed":
			job.Failed++
		default:
			continue
		}
		job.Examined++
	}
}
func (l *Library) metadataWorkerError(category string, ctx context.Context) {
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if l.metadataJob == nil {
		return
	}
	if ctx.Err() != nil {
		if l.metadataJob.Status != "paused" {
			l.metadataJob.Status = "queued"
		}
	} else {
		l.metadataJob.Status = "paused_error"
		l.metadataJob.Error = category
	}
	if l.vault.Unlocked() {
		_ = l.saveMetadataLocked()
	}
}
