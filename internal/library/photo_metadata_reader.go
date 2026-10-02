package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

func (r *metadataResolver) openReader(ctx context.Context) {
	// Shared-object storage already serves authenticated ranges without spawning
	// Restic. Wrapped test/custom backends keep their existing read semantics.
	if _, ok := r.lib.backend.(*resticBackend); !ok {
		return
	}
	if os.Getenv("WEAZLCLOUD_METADATA_READER") == "off" {
		return
	}
	binary, err := exec.LookPath("weazl-restic-reader")
	if err != nil {
		return
	}
	budget := min(int64(2<<30), previewPolicy.MemoryBytes/2)
	// Tiny installations retain the bounded CLI fallback. The session shares
	// process-wide admission with previews; its heap target is not a hard RSS cap.
	if budget < 256<<20 {
		return
	}
	// Reserve child heap and all simultaneous protocol buffers together. Readers
	// must not each reserve the last available byte and then wait for read memory.
	release, err := previewMemory.acquireBackground(ctx, budget+int64(metadataWorkers())*(32<<20))
	if err != nil {
		return
	}
	password, drive, err := r.lib.vault.Secrets()
	clear(drive)
	if err != nil {
		release()
		return
	}
	started := time.Now()
	reader, err := restic.StartReader(ctx, binary, restic.Repo{Location: r.lib.repo, Password: password}, metadataWorkers(), budget)
	clear(password)
	if err != nil {
		release()
		if ctx.Err() == nil {
			log.Printf("photo metadata: persistent reader unavailable; using bounded fallback")
		}
		return
	}
	r.reader, r.releaseReader = reader, release
	log.Printf("photo metadata: persistent reader ready workers=%d index_open_ms=%d memory_allowance=%d", metadataWorkers(), time.Since(started).Milliseconds(), budget)
}

func (r *metadataResolver) closeReader() {
	if r.reader != nil {
		r.reader.Close()
		r.releaseReader()
	}
}

func (r *metadataResolver) readPersistent(ctx context.Context, file catalog.File, limit int64) ([]byte, error) {
	l := r.lib
	l.mu.Lock()
	err := l.validatePreviewLocked(ctx, file)
	var ref catalog.Reference
	var release func()
	if err == nil {
		ref, err = l.capture(file)
	}
	if err == nil {
		release, err = l.holdReference(ref)
	}
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer release()
	select {
	case thumbnailReaders <- struct{}{}:
		defer func() { <-thumbnailReaders }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result, err := r.reader.Read(ctx, ref.Snapshot, ref.Object, int(min(file.Size, limit)))
	if err != nil {
		return nil, err
	}
	valid := result.Size == uint64(file.Size) && int64(len(result.Body)) == min(file.Size, limit)
	if valid && file.Size <= limit {
		hash := sha256.Sum256(result.Body)
		valid = hex.EncodeToString(hash[:]) == file.Hash
	}
	if !valid {
		clear(result.Body)
		return nil, restic.ErrReader
	}
	if err = l.validatePreview(ctx, file); err != nil {
		clear(result.Body)
		return nil, err
	}
	return result.Body, nil
}
