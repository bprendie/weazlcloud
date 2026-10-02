package library

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

var (
	errPhotoFailureRecord    = errors.New("cannot checkpoint preview failure")
	ErrPhotoPreviouslyFailed = errors.New("preview previously failed")
)

const maxPhotoFailureRecords = 100_000

func (l *Library) photoFailurePath(key string) string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-preview-failures", key+".enc")
}

func (l *Library) photoFailure(key string) error {
	f, err := os.Open(l.photoFailurePath(key))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
	}
	if len(raw) > 4096 {
		return fmt.Errorf("%w: invalid record", errPhotoFailureRecord)
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
	}
	defer clear(plain)
	return fmt.Errorf("%w: %s", ErrPhotoPreviouslyFailed, plain)
}

func (l *Library) savePhotoFailure(key string, failure error) error {
	l.photoFailureMu.Lock()
	defer l.photoFailureMu.Unlock()
	if !l.photoFailuresKnown {
		entries, err := os.ReadDir(filepath.Dir(l.photoFailurePath(key)))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
		}
		l.photoFailureCount = len(entries)
		l.photoFailuresKnown = true
	}
	_, statErr := os.Stat(l.photoFailurePath(key))
	isNew := os.IsNotExist(statErr)
	if isNew && l.photoFailureCount >= maxPhotoFailureRecords {
		return fmt.Errorf("%w: failure record limit reached", errPhotoFailureRecord)
	}
	message := failure.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	plain := []byte(time.Now().UTC().Format(time.RFC3339) + ": " + message)
	defer clear(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(l.photoFailurePath(key)), 0o700)
	}
	if err == nil {
		err = cryptox.AtomicWrite(l.photoFailurePath(key), wrapped, 0o600)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
	}
	if isNew {
		l.photoFailureCount++
	}
	return nil
}

func (l *Library) clearPhotoFailure(key string) error {
	l.photoFailureMu.Lock()
	defer l.photoFailureMu.Unlock()
	err := os.Remove(l.photoFailurePath(key))
	if err == nil && l.photoFailuresKnown {
		l.photoFailureCount--
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("%w: %v", errPhotoFailureRecord, err)
	}
	return nil
}

func (l *Library) previewCached(key string) bool {
	info, err := os.Stat(filepath.Join(l.thumbnailDir(), key+".enc"))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= thumbnailMaxOutput
}

// Readiness is a cache inventory, not a count of historic render attempts.
func (l *Library) reconcilePreparedCacheLocked() {
	epoch := thumbnailCacheEpoch.Load()
	if l.photoPrepRunning || l.photoPrepared == nil || l.photoCacheEpoch == epoch {
		return
	}
	ready, bundles := 0, 0
	for key, count := range l.photoPrepared {
		if l.validCachedPreview(key) {
			ready += count
			if viewer := l.photoPreparedBundles[key]; viewer != "" && l.validCachedPreview(viewer) {
				bundles += count
			}
		}
	}
	l.photoPrep.Ready, l.photoPrep.BundleReady = ready, bundles
	if l.photoPrep.Status == "complete" && (ready < l.photoPrep.Total || l.photoPreparedBundles != nil && bundles < l.photoPrep.Total) {
		l.photoPrep.Status = "partial"
	}
	l.photoCacheEpoch = epoch
	l.touchPhotoPreparationLocked()
}
