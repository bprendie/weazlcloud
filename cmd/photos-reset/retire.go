package main

import (
	"context"
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

func retireSnapshots(v *vault.Vault, root string, c *catalog.Catalog, report string, prune bool) error {
	var plan struct {
		Before inventory `json:"before"`
		Retire []string  `json:"retire_snapshots"`
	}
	raw, err := os.ReadFile(filepath.Join(report, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return err
	}
	currentRaw, err := os.ReadFile(filepath.Join(root, "catalog.enc"))
	if err != nil {
		return err
	}
	current, _ := inspect(c, currentRaw, filepath.Base(root))
	if current.Owner != plan.Before.Owner || current.PhotoEntries != 0 || current.DriveSHA != plan.Before.DriveSHA {
		return errors.New("retirement plan does not match reset owner catalog")
	}
	protected := map[string]bool{}
	for _, f := range c.All() {
		protected[f.Snap] = true
		if f.Reference != nil && f.Reference.Backend == catalog.ResticBackend {
			protected[f.Reference.Snapshot] = true
		}
	}
	for _, id := range plan.Retire {
		b, err := hex.DecodeString(id)
		if err != nil || len(b) != 32 || protected[id] {
			return errors.New("unsafe snapshot retirement plan")
		}
	}
	password, drive, err := v.Secrets()
	if err != nil {
		return err
	}
	defer clear(password)
	clear(drive)
	repo := restic.Repo{Location: filepath.Join(root, "library"), Password: password}
	runner := restic.New()
	runner.Stderr = os.Stderr
	ctx := context.Background()
	existing, err := runner.Snapshots(ctx, repo)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, id := range existing {
		live[id] = true
	}
	var ids []string
	for _, id := range plan.Retire {
		if live[id] {
			ids = append(ids, id)
		}
	}
	// Bound argv and avoid a prune for each snapshot batch.
	for len(ids) > 0 {
		n := min(len(ids), 500)
		if err := runner.Run(ctx, repo, nil, os.Stdout, append([]string{"forget"}, ids[:n]...)...); err != nil {
			return err
		}
		ids = ids[n:]
	}
	if prune {
		if err := runner.Run(ctx, repo, nil, os.Stdout, "prune", "--max-repack-size", "1G"); err != nil {
			return err
		}
	}
	return cryptox.AtomicWrite(filepath.Join(report, "snapshots-retired.json"), []byte("{\"complete\":true}\n"), 0600)
}
