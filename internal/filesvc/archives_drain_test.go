package filesvc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveDrainWaitsForCancelledWorkerBeforeRemovingItsOutput(t *testing.T) {
	root := t.TempDir()
	manager := &ArchiveManager{root: root, jobs: make(map[string]*archiveJob)}
	jobCtx, cancel := context.WithCancel(context.Background())
	job := &archiveJob{cancel: cancel, done: make(chan struct{}), path: filepath.Join(root, "late.zip")}
	manager.jobs["job"] = job
	manager.workers.Add(1)
	started := make(chan struct{})
	finish := make(chan struct{})
	go func() {
		defer manager.workers.Done()
		defer close(job.done)
		close(started)
		<-finish
		_ = os.WriteFile(job.path, []byte("worker finished"), 0o600)
	}()
	<-started
	drained := make(chan error, 1)
	go func() { drained <- manager.Drain(context.Background()) }()
	<-jobCtx.Done()
	select {
	case err := <-drained:
		t.Fatalf("drain returned before the worker stopped: %v", err)
	default:
	}
	close(finish)
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(job.path); !os.IsNotExist(err) {
		t.Fatalf("worker output survived drain: %v", err)
	}
}
