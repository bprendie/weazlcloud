package library

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/photos"
)

const photoJournalRecordLimit = 1 << 20

type photoJournalRecord struct {
	Version  int               `json:"version"`
	Sequence uint64            `json:"sequence"`
	Jobs     []photos.MediaJob `json:"jobs"`
}

func (l *Library) appendPhotoJobJournal(jobs []photos.MediaJob) error {
	next := l.photoJobsSequence + 1
	plain, err := json.Marshal(photoJournalRecord{1, next, jobs})
	if err != nil {
		return err
	}
	defer clear(plain)
	wrapped, err := l.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if len(wrapped) > photoJournalRecordLimit {
		return l.checkpointPhotoJobsLocked()
	}
	file, err := os.OpenFile(l.photoJobJournalPath(), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	frame := make([]byte, 4+len(wrapped))
	binary.BigEndian.PutUint32(frame, uint32(len(wrapped)))
	copy(frame[4:], wrapped)
	if _, err = file.Write(frame); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	l.photoJobsSequence = next
	l.photoJobsJournalBytes += int64(len(frame))
	l.photoJobs.Saved()
	return nil
}

func (l *Library) replayPhotoJobJournal() error {
	file, err := os.OpenFile(l.photoJobJournalPath(), os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		l.photoJobsJournalBytes = 0
		l.photoJobsSnapshot = true
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 16<<20 {
		return errors.New("photo job journal exceeds limit")
	}
	var offset int64
	var previous uint64
	for {
		var header [4]byte
		n, readErr := io.ReadFull(file, header[:])
		if readErr == io.EOF && n == 0 {
			break
		}
		if readErr != nil {
			if readErr == io.ErrUnexpectedEOF {
				if err = truncatePhotoJournal(file, offset); err != nil {
					return err
				}
				break
			}
			return readErr
		}
		length := binary.BigEndian.Uint32(header[:])
		if length == 0 || length > photoJournalRecordLimit {
			return errors.New("invalid photo journal frame")
		}
		raw := make([]byte, length)
		if _, readErr = io.ReadFull(file, raw); readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				if err = truncatePhotoJournal(file, offset); err != nil {
					return err
				}
				break
			}
			return readErr
		}
		plain, err := l.vault.Unwrap(raw)
		if err != nil {
			return errors.New("photo journal authentication failed")
		}
		var record photoJournalRecord
		err = json.Unmarshal(plain, &record)
		clear(plain)
		if err != nil || record.Version != 1 || record.Sequence == 0 || len(record.Jobs) > 1024 {
			return errors.New("invalid photo journal record")
		}
		if previous != 0 && record.Sequence != previous+1 {
			return errors.New("photo journal sequence gap")
		}
		previous = record.Sequence
		for _, job := range record.Jobs {
			if !validPhotoJob(job) {
				return errors.New("invalid journal job")
			}
		}
		if record.Sequence > l.photoJobsSequence {
			if record.Sequence != l.photoJobsSequence+1 {
				return errors.New("photo journal does not follow snapshot")
			}
			for _, job := range record.Jobs {
				if _, exists := l.photoJobs.Get(job.ID); !exists && len(l.photoJobs.Jobs) >= photoJobCountLimit {
					return errors.New("photo queue capacity exceeded")
				}
				l.photoJobs.Replace(job)
			}
			l.photoJobsSequence = record.Sequence
		}
		offset += int64(4 + length)
	}
	l.photoJobsJournalBytes = offset
	return nil
}

func truncatePhotoJournal(file *os.File, offset int64) error {
	if err := file.Truncate(offset); err != nil {
		return err
	}
	return file.Sync()
}
