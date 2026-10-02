package library

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestThumbnailStagePerformanceRecord(t *testing.T) {
	if os.Getenv("WEAZLCLOUD_MEASURE_PREVIEWS") != "1" {
		t.Skip("opt-in stage sample")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	l := newPhotoIndexTestLibrary(t)
	l.backend = newResticBackend(l.repo, l.vault)
	ctx, release := l.previewContext(context.Background())
	defer release()
	var original bytes.Buffer
	_ = jpeg.Encode(&original, image.NewRGBA(image.Rect(0, 0, 1200, 800)), &jpeg.Options{Quality: 85})
	f, err := l.Put(ctx, "Photos/stages.jpg", original.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	f, err = l.Metadata(ctx, f.Path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	reader, releaseReader := l.borrowPreviewReader(ctx)
	opened := time.Since(start)
	defer releaseReader()
	t.Cleanup(func() { l.vault.Lock(); l.stopPreviewReader() })
	t.Logf("reader_open_ms=%.3f persistent=%t", float64(opened.Microseconds())/1000, reader != nil)
	start = time.Now()
	source, err := l.thumbnailSource(ctx, f, f.Size, false)
	read := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(source)
	var outputs []previewrpc.Variant
	start = time.Now()
	err = renderPhotoBundle(ctx, source, []int{320, 1280}, "raster", func(v previewrpc.Variant) error {
		outputs = append(outputs, v)
		t.Logf("render_variant=%d elapsed_ms=%.3f bytes=%d", v.Size, float64(time.Since(start).Microseconds())/1000, len(v.Body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("authenticated_source_bytes=%d read_ms=%.3f", len(source), float64(read.Microseconds())/1000)
	for _, v := range outputs {
		env := thumbnailEnvelope{ContentType: v.MIME, Body: v.Body, ThumbHash: v.ThumbHash, Size: v.Size}
		start = time.Now()
		plain, _ := json.Marshal(env)
		wrapped, err := l.vault.Wrap(plain)
		clear(plain)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("variant=%d marshal_encrypt_ms=%.3f encrypted_bytes=%d", v.Size, float64(time.Since(start).Microseconds())/1000, len(wrapped))
		key, _ := thumbnailKey(l.vault, f, v.Size)
		start = time.Now()
		if err := l.writeThumbnailCache(key, env); err != nil {
			t.Fatal(err)
		}
		t.Logf("variant=%d durable_cache_write_with_encryption_ms=%.3f", v.Size, float64(time.Since(start).Microseconds())/1000)
	}
	l.photoJobsMu.Lock()
	defer l.photoJobsMu.Unlock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 37082; i++ {
		l.photoJobs.Upsert(photos.NewMediaJob(l.ownerID, string(rune(i+1)), 1, photoJobOperation, thumbnailRenderer, 3))
	}
	start = time.Now()
	if err := l.savePhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	t.Logf("queue_snapshot_ms=%.3f", float64(time.Since(start).Microseconds())/1000)
	jobs := l.photoJobs.Lease("measure", time.Now(), time.Minute, 1)
	start = time.Now()
	if err := l.savePhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	t.Logf("one_lease_journal_ms=%.3f", float64(time.Since(start).Microseconds())/1000)
	l.photoJobs.Complete(jobs[0].ID, "measure")
	start = time.Now()
	if err := l.savePhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(l.photoJobJournalPath())
	t.Logf("one_completion_journal_ms=%.3f journal_bytes=%d", float64(time.Since(start).Microseconds())/1000, info.Size())
}
