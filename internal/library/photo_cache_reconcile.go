package library

import (
	"context"
	"maps"
)

// Called with photoPrepMu held. Decrypting every cached derivative can take
// minutes on a large vault; do it once in background, never in a status poll.
func (l *Library) reconcilePreparedCacheLocked() {
	epoch := thumbnailCacheEpoch.Load()
	if l.photoCacheChecking || l.photoPrepRunning || l.photoPrepared == nil || l.photoCacheEpoch == epoch {
		return
	}
	ctx, release := l.previewContext(context.Background())
	if ctx.Err() != nil {
		release()
		return
	}
	prepared, viewers := maps.Clone(l.photoPrepared), maps.Clone(l.photoPreparedBundles)
	generation, oldEpoch := l.photoPrep.Generation, l.photoCacheEpoch
	l.photoCacheChecking = true
	go func() {
		defer release()
		ready, bundles := 0, 0
		for key, count := range prepared {
			if ctx.Err() != nil || thumbnailCacheEpoch.Load() != epoch {
				break
			}
			if l.validCachedPreview(key) {
				ready += count
				if viewer := viewers[key]; viewer != "" && l.validCachedPreview(viewer) {
					bundles += count
				}
			}
		}
		l.photoPrepMu.Lock()
		defer l.photoPrepMu.Unlock()
		l.photoCacheChecking = false
		// A render pass, eviction, rekey or lock may supersede this snapshot.
		if ctx.Err() != nil || thumbnailCacheEpoch.Load() != epoch || l.photoPrepRunning || l.photoPrep.Generation != generation || l.photoCacheEpoch != oldEpoch {
			return
		}
		l.photoPrep.Ready, l.photoPrep.BundleReady = ready, bundles
		if l.photoPrep.Status == "complete" && (ready < l.photoPrep.Total || viewers != nil && bundles < l.photoPrep.Total) {
			l.photoPrep.Status = "partial"
		}
		l.photoCacheEpoch = epoch
		l.touchPhotoPreparationLocked()
	}()
}
