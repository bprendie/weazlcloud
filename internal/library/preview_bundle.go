package library

import (
	"context"
	"errors"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type assetPreviewVariant struct {
	done  chan struct{}
	ready bool
	body  []byte
	mime  string
	err   error
}
type assetPreviewJob struct {
	done       chan struct{}
	variants   map[int]*assetPreviewVariant
	waiters    int
	background bool
	sealed     bool
	promote    chan struct{}
	cancel     context.CancelFunc
	progress   map[int]func(int)
	next       int
}

func (l *Library) bundlePreview(ctx context.Context, f catalog.File, size int, background bool) ([]byte, string, error) {
	key, err := thumbnailKey(l.vault, f, 0)
	if err != nil {
		return nil, "", err
	}
	l.thumbMu.Lock()
	if l.bundleJobs == nil {
		l.bundleJobs = map[string]*assetPreviewJob{}
	}
	job := l.bundleJobs[key]
	if job != nil && (job.waiters == 0 || job.sealed) {
		l.thumbMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-job.done:
		}
		return l.bundlePreview(ctx, f, size, background)
	}
	start := job == nil
	if start {
		select {
		case previewQueued <- struct{}{}:
		default:
			l.thumbMu.Unlock()
			return nil, "", errPreviewBusy
		}
		job = &assetPreviewJob{done: make(chan struct{}), variants: map[int]*assetPreviewVariant{}, background: background, promote: make(chan struct{})}
		l.bundleJobs[key] = job
	}
	ensure := func(size int) *assetPreviewVariant {
		v := job.variants[size]
		if v == nil {
			v = &assetPreviewVariant{done: make(chan struct{})}
			job.variants[size] = v
		}
		return v
	}
	additional := 0
	needed := map[int]bool{size: true}
	if background {
		needed[320], needed[1280] = true, true
	}
	for n := range needed {
		if job.variants[n] == nil {
			additional++
		}
	}
	if len(job.variants)+additional > 8 {
		l.thumbMu.Unlock()
		return nil, "", errPreviewBusy
	}
	wanted := ensure(size)
	var viewer *assetPreviewVariant
	if background {
		ensure(320)
		viewer = ensure(1280)
	}
	job.waiters++
	job.next++
	waiterID := job.next
	if update, ok := ctx.Value(photoProgressContextKey{}).(func(int)); ok {
		if job.progress == nil {
			job.progress = map[int]func(int){}
		}
		job.progress[waiterID] = update
	}
	if !background && job.background {
		close(job.promote)
		job.background = false
	}
	if start {
		work, release := l.previewContext(context.Background())
		work, cancel := context.WithCancel(work)
		job.cancel = cancel
		work = withPhotoProgress(work, func(value int) {
			l.thumbMu.Lock()
			var callbacks []func(int)
			for _, update := range job.progress {
				callbacks = append(callbacks, update)
			}
			l.thumbMu.Unlock()
			for _, update := range callbacks {
				update(value)
			}
		})
		go func() {
			defer func() { <-previewQueued }()
			defer release()
			defer cancel()
			err := l.generateBundle(work, f, job)
			l.thumbMu.Lock()
			for _, v := range job.variants {
				if !v.ready {
					v.err = err
					if err == nil {
						v.err = ErrThumbnailUnavailable
					}
					v.ready = true
					close(v.done)
				}
			}
			delete(l.bundleJobs, key)
			close(job.done)
			l.thumbMu.Unlock()
		}()
	}
	l.thumbMu.Unlock()
	defer func() {
		l.thumbMu.Lock()
		job.waiters--
		delete(job.progress, waiterID)
		if job.waiters == 0 {
			job.cancel()
		}
		l.thumbMu.Unlock()
	}()
	if background {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-job.done:
		}
		if viewer.err != nil {
			return nil, "", viewer.err
		}
	} else {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-wanted.done:
		}
	}
	if wanted.err != nil && !(!background && errors.Is(wanted.err, ErrPreviewCacheSkipped)) {
		return nil, "", wanted.err
	}
	return wanted.body, wanted.mime, nil
}

func (l *Library) finishBundleVariant(job *assetPreviewJob, size int, body []byte, mime string, err error) {
	l.thumbMu.Lock()
	defer l.thumbMu.Unlock()
	v := job.variants[size]
	if v != nil && !v.ready {
		v.body, v.mime, v.err, v.ready = body, mime, err, true
		close(v.done)
	}
}

func (l *Library) pendingBundle(job *assetPreviewJob, seal bool) []int {
	l.thumbMu.Lock()
	defer l.thumbMu.Unlock()
	var sizes []int
	for size, v := range job.variants {
		if !v.ready {
			sizes = append(sizes, size)
		}
	}
	if seal && len(sizes) == 0 {
		job.sealed = true
	}
	return sizes
}
