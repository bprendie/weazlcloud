package library

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Version 1 remains readable by the previous image; Sequence is additive. An
// unlocked clean drain exports the latest queue here for image rollback.
type photoJobStoreFile struct {
	Version  int             `json:"version"`
	Queue    photos.JobQueue `json:"queue"`
	Sequence uint64          `json:"sequence,omitempty"`
}

func (l *Library) photoJobStorePath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-jobs.enc")
}
func (l *Library) photoJobJournalPath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-jobs.journal.enc")
}
func (l *Library) clearPhotoJobMemory() {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	clear(l.photoJobs.Jobs)
	l.photoJobs = photos.JobQueue{}
	l.photoJobsLoaded = false
	l.photoJobsSequence = 0
	l.photoJobsJournalBytes = 0
	l.photoJobsSnapshot = false
}

func (l *Library) loadPhotoJobsLocked() error {
	if l.photoJobsLoaded {
		return nil
	}
	file, err := os.Open(l.photoJobStorePath())
	if errors.Is(err, os.ErrNotExist) {
		if _, journalErr := os.Stat(l.photoJobJournalPath()); !errors.Is(journalErr, os.ErrNotExist) {
			return errors.New("photo job snapshot is missing")
		}
		l.photoJobs = photos.JobQueue{}
		l.photoJobsSequence = 0
		l.photoJobsJournalBytes = 0
		l.photoJobsLoaded = true
		l.photoJobsSnapshot = true
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, photoJobStoreLimit+1))
	if err != nil || len(raw) > photoJobStoreLimit {
		return errors.New("photo job snapshot exceeds its limit")
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return err
	}
	defer clear(plain)
	var disk photoJobStoreFile
	if json.Unmarshal(plain, &disk) != nil || disk.Version != photoJobStoreVersion || len(disk.Queue.Jobs) > photoJobCountLimit {
		return errors.New("invalid photo job snapshot")
	}
	ids := map[string]bool{}
	for _, job := range disk.Queue.Jobs {
		if !validPhotoJob(job) || ids[job.ID] {
			return errors.New("invalid photo job")
		}
		ids[job.ID] = true
	}
	l.photoJobs = disk.Queue
	l.photoJobs.Reindex()
	l.photoJobsSequence = disk.Sequence
	l.photoJobsSnapshot = false
	if err = l.replayPhotoJobJournal(); err != nil {
		l.photoJobs = photos.JobQueue{}
		return err
	}
	l.photoJobs.Saved()
	// Recover leases once per owner instance, not once per dispatch wave.
	l.photoJobs.RequeueLeasesExcept("")
	l.photoJobsLoaded = true
	return nil
}

func (l *Library) savePhotoJobsLocked() (err error) {
	defer func() {
		if err != nil {
			l.photoJobsLoaded = false
		}
	}()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if len(l.photoJobs.Jobs) > photoJobCountLimit {
		return errors.New("photo queue capacity exceeded")
	}
	changes := l.photoJobs.Changes()
	if l.photoJobsSnapshot || l.photoJobsJournalBytes >= 8<<20 || len(changes) > 1024 {
		return l.checkpointPhotoJobsLocked()
	}
	if len(changes) == 0 {
		return nil
	}
	return l.appendPhotoJobJournal(changes)
}

func (l *Library) checkpointPhotoJobsLocked() error {
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	next := l.photoJobsSequence + 1
	plain, err := json.Marshal(photoJobStoreFile{Version: photoJobStoreVersion, Queue: l.photoJobs, Sequence: next})
	if err != nil {
		return err
	}
	defer clear(plain)
	if len(plain) > photoJobStoreLimit-1024 {
		return errors.New("photo queue capacity exceeded")
	}
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if err = durablePhotoJobWrite(l.photoJobStorePath(), wrapped); err != nil {
		return err
	}
	l.photoJobsSequence = next
	// A crash between these writes leaves an older authenticated journal whose
	// records are already included in the new snapshot and are safely skipped.
	if err = durablePhotoJobWrite(l.photoJobJournalPath(), nil); err != nil {
		return err
	}
	l.photoJobsJournalBytes = 0
	l.photoJobsSnapshot = false
	l.photoJobs.Saved()
	return nil
}

func (l *Library) exportPhotoJobs() error {
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if !l.vault.Unlocked() || !l.photoJobsLoaded {
		return nil
	}
	return l.checkpointPhotoJobsLocked()
}

func durablePhotoJobWrite(path string, data []byte) error {
	if err := cryptox.AtomicWrite(path, data, 0600); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
