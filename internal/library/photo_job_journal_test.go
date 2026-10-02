package library

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/bprendie/weazlcloud/internal/photos"
)

func journalFixture(t *testing.T) (*Library, photos.MediaJob) {
	t.Helper()
	l := newPhotoIndexTestLibrary(t)
	if _, err := l.syncPhotoJobs([]photoJobFile{{id: "private-asset", revision: 1}}, false); err != nil {
		t.Fatal(err)
	}
	jobs, err := l.leasePhotoJobs("worker", 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	return l, jobs[0]
}

func TestPhotoJournalIncrementalRecoveryAndRollbackExport(t *testing.T) {
	l, job := journalFixture(t)
	before, err := os.ReadFile(l.photoJobStorePath())
	if err != nil {
		t.Fatal(err)
	}
	if err = l.settlePhotoJobs([]photoJobResult{{job: job}}, "worker"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(l.photoJobStorePath())
	if !bytes.Equal(before, after) {
		t.Fatal("completion rewrote full snapshot")
	}
	journal, err := os.ReadFile(l.photoJobJournalPath())
	if err != nil || len(journal) == 0 {
		t.Fatal(err)
	}
	if bytes.Contains(journal, []byte("private-asset")) {
		t.Fatal("plaintext job in journal")
	}
	l.clearPhotoJobMemory()
	_, _, ready, _, err := l.photoJobCounts()
	if err != nil || ready != 1 {
		t.Fatal("journal replay", ready, err)
	}
	if err = l.exportPhotoJobs(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(l.photoJobStorePath())
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var legacy struct {
		Version int             `json:"version"`
		Queue   photos.JobQueue `json:"queue"`
	}
	if json.Unmarshal(plain, &legacy) != nil || legacy.Version != 1 || legacy.Queue.Jobs[0].Status != photos.JobSucceeded {
		t.Fatal("rollback snapshot stale")
	}
	info, _ := os.Stat(l.photoJobJournalPath())
	if info.Size() != 0 {
		t.Fatal("checkpoint did not compact")
	}
}

func TestPhotoJournalTornTailAndCorruptInterior(t *testing.T) {
	t.Run("tail", func(t *testing.T) {
		l, _ := journalFixture(t)
		old, _ := os.ReadFile(l.photoJobJournalPath())
		file, err := os.OpenFile(l.photoJobJournalPath(), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write([]byte{0, 1})
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		l.clearPhotoJobMemory()
		pending, _, _, _, err := l.photoJobCounts()
		if err != nil || pending != 1 {
			t.Fatal("tail recovery", err)
		}
		next, _ := os.ReadFile(l.photoJobJournalPath())
		if !bytes.Equal(old, next) {
			t.Fatal("recovery altered complete records")
		}
	})
	t.Run("authenticated-record", func(t *testing.T) {
		l, _ := journalFixture(t)
		raw, _ := os.ReadFile(l.photoJobJournalPath())
		raw[len(raw)/2] ^= 1
		if err := os.WriteFile(l.photoJobJournalPath(), raw, 0600); err != nil {
			t.Fatal(err)
		}
		l.clearPhotoJobMemory()
		if _, _, _, _, err := l.photoJobCounts(); err == nil {
			t.Fatal("corrupt record accepted")
		}
	})
}

func TestPhotoJournalMigratesLegacyAndDoesNotStealLiveLease(t *testing.T) {
	l, job := journalFixture(t)
	if next, err := l.leasePhotoJobs("other-live-worker", 1); err != nil || len(next) != 0 {
		t.Fatal("live lease stolen", err)
	}
	if err := l.exportPhotoJobs(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(l.photoJobJournalPath()); err != nil {
		t.Fatal(err)
	}
	l.clearPhotoJobMemory()
	leased, err := l.leasePhotoJobs("restart", 1)
	if err != nil || len(leased) != 1 || leased[0].ID != job.ID {
		t.Fatal("legacy migration", err)
	}
	if _, err = os.Stat(l.photoJobJournalPath()); err != nil {
		t.Fatal("journal not initialized", err)
	}
}

func TestPhotoJournalWriteFailureDoesNotCommitSuccess(t *testing.T) {
	l, job := journalFixture(t)
	old, _ := os.ReadFile(l.photoJobJournalPath())
	if err := os.Remove(l.photoJobJournalPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(l.photoJobJournalPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := l.settlePhotoJobs([]photoJobResult{{job: job}}, "worker"); err == nil {
		t.Fatal("failed checkpoint accepted")
	}
	if err := os.Remove(l.photoJobJournalPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.photoJobJournalPath(), old, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, ready, _, err := l.photoJobCounts()
	if err != nil || ready != 0 {
		t.Fatal("uncommitted success retained", ready, err)
	}
}
