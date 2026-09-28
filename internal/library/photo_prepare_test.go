package library

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhotoPreparationIsExplicitResumableAndEncrypted(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Photos/a.png", "Photos/b.png"} {
		if _, err := l.Put(context.Background(), name, source.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	status, err := l.PhotoPreparation()
	if err != nil || status.Enabled {
		t.Fatalf("preparation was not opt-in: %+v, %v", status, err)
	}
	l.setStageActive("import", true)
	status, err = l.SetPhotoPreparation(context.Background(), "start")
	if err != nil || !status.Enabled {
		t.Fatalf("start status %+v, %v", status, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err = l.PhotoPreparation()
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "paused_storage" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("preparation stalled: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	l.setStageActive("import", false)
	deadline = time.Now().Add(10 * time.Second)
	for {
		status, err = l.PhotoPreparation()
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("preparation did not resume after storage work: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.Total != 2 || status.Ready != 2 || status.Failed != 0 {
		t.Fatalf("wrong completion counters: %+v", status)
	}
	raw, err := os.ReadFile(l.photoPreparationPath())
	if err != nil || bytes.Contains(raw, []byte("Photos/a.png")) {
		t.Fatalf("preparation state not encrypted: %v", err)
	}
	if _, err := l.SetPhotoPreparation(context.Background(), "pause"); err != nil {
		t.Fatal(err)
	}
	status, _ = l.PhotoPreparation()
	if status.Status != "paused" || !status.Enabled {
		t.Fatalf("pause status %+v", status)
	}
	if _, err := l.SetPhotoPreparation(context.Background(), "resume"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		status, err = l.PhotoPreparation()
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("explicit resume did not finish: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPhotoPreparationInvalidAction(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	if _, err := l.SetPhotoPreparation(context.Background(), "delete"); err != ErrPhotoPreparationAction {
		t.Fatalf("bad action error %v", err)
	}
	if filepath.Base(l.photoPreparationPath()) == "" {
		t.Fatal("missing private state path")
	}
}
