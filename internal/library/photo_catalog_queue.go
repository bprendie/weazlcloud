package library

import (
	"context"
	"log"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type photoCommitGroupKey struct{}

// WithPhotoCommitGroup is only for trusted internal callers whose identical keys
// guarantee equivalent authorization guards (including owner/device epoch,
// expiry and scopes). Never derive this key from a client-provided batch label.
func WithPhotoCommitGroup(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, photoCommitGroupKey{}, key)
}

type photoCatalogRequest struct {
	ctx     context.Context
	session uint64
	file    catalog.File
	commit  catalog.PhotoIngestCommit
	group   string
	guard   func(func() error) error
	done    chan catalog.PhotoBatchResult
}

func (l *Library) enqueuePhotoCatalog(r photoCatalogRequest) (catalog.File, error) {
	r.done = make(chan catalog.PhotoBatchResult, 1)
	l.photoCatalogMu.Lock()
	l.photoCatalogPending = append(l.photoCatalogPending, r)
	if !l.photoCatalogRunning {
		l.photoCatalogRunning = true
		go l.runPhotoCatalog()
	}
	l.photoCatalogMu.Unlock()
	// Retain the caller's lifecycle/storage lease until its transaction completes.
	result := <-r.done
	return result.File, result.Err
}

func (l *Library) runPhotoCatalog() {
	for {
		time.Sleep(20 * time.Millisecond)
		l.photoCatalogMu.Lock()
		if len(l.photoCatalogPending) == 0 {
			l.photoCatalogRunning = false
			l.photoCatalogMu.Unlock()
			return
		}
		requests := l.photoCatalogPending
		l.photoCatalogPending = nil
		l.photoCatalogMu.Unlock()
		// Component puts share no publication grant. Final commits only combine
		// identical trusted grant groups; component/final batches remain separate.
		for len(requests) > 0 {
			first := requests[0]
			var batch, rest []photoCatalogRequest
			for _, r := range requests {
				if len(batch) < 8 && (r.file.Path != "") == (first.file.Path != "") && r.group == first.group && r.session == first.session {
					batch = append(batch, r)
				} else {
					rest = append(rest, r)
				}
			}
			l.applyPhotoCatalog(batch)
			requests = rest
		}
	}
}

func (l *Library) applyPhotoCatalog(requests []photoCatalogRequest) {
	started := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	unlocked, session := l.vault.State()
	var live []photoCatalogRequest
	for _, r := range requests {
		err := r.ctx.Err()
		if err == nil && (!unlocked || session != r.session) {
			err = vault.ErrLocked
		}
		if err != nil {
			r.done <- catalog.PhotoBatchResult{Err: err}
		} else {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		return
	}
	if err := l.loadCatalogSession(session); err != nil {
		for _, r := range live {
			r.done <- catalog.PhotoBatchResult{Err: err}
		}
		return
	}
	var results []catalog.PhotoBatchResult
	publish := func() error {
		if live[0].file.Path != "" {
			files := make([]catalog.File, len(live))
			for i, r := range live {
				files[i] = r.file
			}
			results = l.catalog.PutPhotoComponents(files)
		} else {
			commits := make([]catalog.PhotoIngestCommit, len(live))
			for i, r := range live {
				commits[i] = r.commit
			}
			results = l.catalog.CommitPhotoIngestBatch(commits)
		}
		return nil
	}
	var err error
	if live[0].guard != nil {
		err = live[0].guard(publish)
	} else {
		err = publish()
	}
	paths := []string{}
	success := 0
	for i, r := range live {
		result := catalog.PhotoBatchResult{Err: err}
		if err == nil {
			result = results[i]
		}
		if result.Err == nil {
			success++
			if r.file.Path == "" {
				for _, part := range r.commit.Files {
					paths = append(paths, part.From, part.To)
				}
			}
		}
	}
	if len(paths) > 0 {
		l.publishChange(Change{Kind: "put", Paths: paths})
	}
	for i, r := range live {
		result := catalog.PhotoBatchResult{Err: err}
		if err == nil {
			result = results[i]
		}
		r.done <- result
	}
	kind := "component"
	if live[0].file.Path == "" {
		kind = "publish"
	}
	log.Printf("mobile catalog batch kind=%s items=%d stored=%d duration_ms=%d", kind, len(live), success, time.Since(started).Milliseconds())
}
