package photos

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type MigrationOptions struct {
	CheckpointPath string
	DryRun         bool
}

type MigrationReport struct {
	Examined   int
	Updated    int
	Skipped    int
	Unresolved int
	Errors     int
}

type MetadataResult struct {
	Capture *Capture
	Media   *MediaMetadata
}

func (r MetadataResult) Found() bool {
	return r.Capture != nil || r.Media != nil
}

type checkpoint struct {
	Version   int
	Completed map[string]uint64
}

func RunMigration(ctx context.Context, assets []Asset, options MigrationOptions, resolve func(context.Context, Asset) (Capture, bool, error), apply func(Asset, Capture) error) (MigrationReport, error) {
	return RunMetadataMigration(ctx, assets, options, func(resolveCtx context.Context, asset Asset) (MetadataResult, error) {
		capture, found, err := resolve(resolveCtx, asset)
		if err != nil || !found {
			return MetadataResult{}, err
		}
		return MetadataResult{Capture: &capture}, nil
	}, func(asset Asset, result MetadataResult) error {
		if result.Capture == nil {
			return nil
		}
		return apply(asset, *result.Capture)
	})
}

func RunMetadataMigration(ctx context.Context, assets []Asset, options MigrationOptions, resolve func(context.Context, Asset) (MetadataResult, error), apply func(Asset, MetadataResult) error) (MigrationReport, error) {
	state, err := loadCheckpoint(options.CheckpointPath)
	if err != nil {
		return MigrationReport{}, err
	}
	report := MigrationReport{}
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Examined++
		if done, ok := state.Completed[asset.ID]; ok && done >= asset.Revision {
			report.Skipped++
			continue
		}
		result, resolveErr := resolve(ctx, asset)
		if resolveErr != nil {
			report.Errors++
			continue
		}
		if !result.Found() {
			report.Unresolved++
			continue
		}
		if !options.DryRun {
			if err := apply(asset, result); err != nil {
				report.Errors++
				continue
			}
		}
		report.Updated++
		if !options.DryRun {
			if err := writeCheckpoint(options.CheckpointPath, state, asset); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func loadCheckpoint(path string) (checkpoint, error) {
	state := checkpoint{Version: 1, Completed: make(map[string]uint64)}
	if path == "" {
		return state, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Version != 1 || state.Completed == nil {
		return checkpoint{}, errors.New("invalid photo metadata checkpoint")
	}
	return state, nil
}

func writeCheckpoint(path string, state checkpoint, asset Asset) error {
	if path == "" {
		return nil
	}
	state.Completed[asset.ID] = asset.Revision
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
