package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func (l *Library) runLiveWorker(ctx context.Context) {
	for ctx.Err() == nil {
		l.liveMu.Lock()
		pending := []int{}
		for i, e := range l.liveJob.Entries {
			if e.Status == "pending" {
				pending = append(pending, i)
				if len(pending) == 16 {
					break
				}
			}
		}
		if len(pending) == 0 {
			l.liveMu.Unlock()
			if l.finishLivePairs(ctx) {
				return
			}
			continue
		}
		entries := make([]LivePhotoEntry, len(pending))
		for i, index := range pending {
			entries[i] = l.liveJob.Entries[index]
		}
		l.liveMu.Unlock()
		var wg sync.WaitGroup
		slots := make(chan struct{}, max(1, min(4, previewPolicy.SourceReaders)))
		for i := range entries {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-ctx.Done():
					return
				}
				entries[i] = l.inspectLiveEntry(ctx, entries[i])
			}(i)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		l.liveMu.Lock()
		if l.liveJob.Status != "running" {
			l.liveMu.Unlock()
			return
		}
		for i, index := range pending {
			l.liveJob.Entries[index] = entries[i]
		}
		l.liveJob.Examined = 0
		l.liveJob.Failed = 0
		for _, e := range l.liveJob.Entries {
			if e.Status != "pending" {
				l.liveJob.Examined++
			}
			if e.Status == "failed" {
				l.liveJob.Failed++
			}
		}
		err := l.saveLiveJobLocked()
		if err != nil {
			l.liveJob.Status = "paused_error"
		}
		l.liveMu.Unlock()
		if err != nil {
			return
		}
	}
}
func (l *Library) inspectLiveEntry(ctx context.Context, e LivePhotoEntry) LivePhotoEntry {
	l.mu.Lock()
	err := l.ensurePhotoIndexLocked(ctx)
	l.photoMu.Lock()
	file, ok := l.photoByID[e.ID]
	l.photoMu.Unlock()
	l.mu.Unlock()
	if err != nil || !ok || !file.Present || file.Hash != e.Hash {
		e.Status = "failed"
		e.Category = "source_changed"
		return e
	}
	e.Path = file.Path
	if file.Size <= 0 || file.Size > 64<<20 {
		e.Status = "failed"
		e.Category = "unsupported_size"
		return e
	}
	release, err := previewMemory.acquireBackground(ctx, sourceAllowance(file)+file.Size*2+8<<20)
	if err != nil {
		e.Status = "failed"
		e.Category = photoJobErrorCategory(err)
		return e
	}
	defer release()
	body, err := l.thumbnailSource(ctx, file, file.Size, false)
	if err != nil {
		e.Status = "failed"
		e.Category = "source_read"
		return e
	}
	defer clear(body)
	var identity LivePhotoIdentity
	socket := os.Getenv("WEAZLCLOUD_PREVIEW_WORKER_SOCKET")
	if socket != "" {
		raw, requestErr := previewrpc.LiveRequest(ctx, socket, "/v1/live-identity?media="+photoPreviewKind(file.Path), body)
		err = requestErr
		if err == nil {
			err = json.Unmarshal(raw, &identity)
		}
		clear(raw)
	} else {
		identity, err = InspectLivePhoto(ctx, body, photoPreviewKind(file.Path))
	}
	if err != nil {
		e.Status = "failed"
		e.Category = photoJobErrorCategory(err)
		return e
	}
	if err = l.validatePreview(ctx, file); err != nil {
		e.Status = "failed"
		e.Category = "source_changed"
		return e
	}
	e.Status = "examined"
	e.Identifier = identity.Identifier
	e.MediaType = identity.MediaType
	e.Category = ""
	return e
}
func (l *Library) finishLivePairs(ctx context.Context) bool {
	l.liveMu.Lock()
	entries := append([]LivePhotoEntry(nil), l.liveJob.Entries...)
	dry := l.liveJob.DryRun
	l.liveMu.Unlock()
	groups := map[string][]LivePhotoEntry{}
	orphans := 0
	for _, e := range entries {
		if e.Status != "examined" {
			continue
		}
		if e.Identifier == "" {
			orphans++
			continue
		}
		key := filepath.Dir(e.Path) + "\x00" + e.Identifier
		groups[key] = append(groups[key], e)
	}
	pairable, paired, ambiguous := 0, 0, 0
	for _, group := range groups {
		if ctx.Err() != nil {
			return true
		}
		if len(group) < 2 {
			orphans += len(group)
			continue
		}
		if len(group) != 2 {
			ambiguous += len(group)
			continue
		}
		a, b := group[0], group[1]
		if strings.HasPrefix(a.MediaType, "video/") {
			a, b = b, a
		}
		if !strings.HasPrefix(a.MediaType, "image/") || !strings.HasPrefix(b.MediaType, "video/") {
			ambiguous += 2
			continue
		}
		l.mu.Lock()
		err := l.ensurePhotoIndexLocked(ctx)
		l.photoMu.Lock()
		still, okA := l.photoByID[a.ID]
		motion, okB := l.photoByID[b.ID]
		hiddenA := l.photoPathHiddenLocked(still.Path)
		hiddenB := l.photoPathHiddenLocked(motion.Path)
		l.photoMu.Unlock()
		l.mu.Unlock()
		if err != nil || !okA || !okB || !still.Present || !motion.Present || still.Hash != a.Hash || motion.Hash != b.Hash || filepath.Dir(still.Path) != filepath.Dir(motion.Path) || hiddenA != hiddenB || still.Favorite != motion.Favorite || still.Archived != motion.Archived || still.Hidden != motion.Hidden {
			ambiguous += 2
			continue
		}
		if motion.PhotoParentID == still.EntryID {
			paired++
			continue
		}
		if motion.PhotoParentID != "" || len(still.PhotoComponents) > 1 {
			ambiguous += 2
			continue
		}
		pairable++
		if !dry {
			if _, err := l.LinkLivePhoto(ctx, still.EntryID, motion.EntryID, still.Revision, motion.Revision, hiddenA, false); err != nil {
				ambiguous += 2
			} else {
				paired++
			}
		}
	}
	l.liveMu.Lock()
	defer l.liveMu.Unlock()
	if ctx.Err() != nil || l.liveJob.Status != "running" {
		return true
	}
	for _, e := range l.liveJob.Entries {
		if e.Status == "pending" {
			return false
		}
	}
	l.liveJob.Pairable, l.liveJob.Paired, l.liveJob.Ambiguous, l.liveJob.Orphan = pairable, paired, ambiguous, orphans
	l.liveJob.Status = "complete"
	if l.liveJob.Failed > 0 || ambiguous > 0 {
		l.liveJob.Status = "complete_with_issues"
	}
	if err := l.saveLiveJobLocked(); err != nil {
		l.liveJob.Status = "paused_error"
	}

	return true
}

func (l *Library) liveMotionFile(ctx context.Context, id string, hidden bool) (catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.File{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	parent, ok := l.photoByID[id]
	if !ok || !parent.Present || l.photoPathHiddenLocked(parent.Path) != hidden {
		return catalog.File{}, catalog.ErrNotFound
	}
	for _, part := range parent.PhotoComponents {
		file, ok := l.photoByID[part.AssetID]
		if part.ID == "motion" && ok && file.Present && file.PhotoParentID == id && l.photoPathHiddenLocked(file.Path) == hidden {
			return file, nil
		}
	}
	return catalog.File{}, catalog.ErrNotFound
}
