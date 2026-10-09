package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func verifyRepository(v *vault.Vault, root string, c *catalog.Catalog, report string, check bool) error {
	password, drive, err := v.Secrets()
	if err != nil {
		return err
	}
	defer clear(password)
	clear(drive)
	r := restic.New()
	r.Stderr = os.Stderr
	repo := restic.Repo{Location: filepath.Join(root, "library"), Password: password}
	ctx := context.Background()
	checkResult := "not_requested"
	if check {
		if err := r.Run(ctx, repo, nil, os.Stdout, "check"); err != nil {
			return err
		}
		checkResult = "passed"
	}
	ids, err := r.Snapshots(ctx, repo)
	if err != nil {
		return err
	}
	snapshots := map[string]bool{}
	for _, id := range ids {
		snapshots[id] = true
	}
	samples, files := 0, 0
	var sampleBytes int64
	for _, f := range c.All() {
		if catalog.PhotoResetPath(f.Path) {
			return errors.New("Photos remained after reset")
		}
		if f.Folder {
			continue
		}
		files++
		snap, object := f.Snap, f.Object
		if object == "" {
			object = f.Hash
		}
		if f.Reference != nil {
			if f.Reference.Backend != catalog.ResticBackend {
				return errors.New("unsupported sample backend")
			}
			snap, object = f.Reference.Snapshot, f.Reference.Object
		}
		if !snapshots[snap] {
			return errors.New("retained file snapshot is missing")
		}
		if samples >= 5 || f.Size <= 0 || f.Size > 1<<20 || f.Hash == "" {
			continue
		}
		hash := sha256.New()
		if err := r.Dump(ctx, repo, snap, object, hash); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != f.Hash {
			return errors.New("retained file sample hash mismatch")
		}
		samples++
		sampleBytes += f.Size
	}
	if samples == 0 && files > 0 {
		return errors.New("no retained file samples verified")
	}
	raw, _ := json.Marshal(map[string]any{"repository_check": checkResult, "retained_files": files, "retained_snapshots": len(ids), "byte_verified_samples": samples, "sample_bytes": sampleBytes})
	if report != "" {
		if err := cryptox.AtomicWrite(filepath.Join(report, "verification.json"), raw, 0600); err != nil {
			return err
		}
	}
	_, err = os.Stdout.Write(append(raw, '\n'))
	return err
}
