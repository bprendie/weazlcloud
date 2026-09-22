package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (r Runner) Init(ctx context.Context, repo Repo) error {
	cfg := filepath.Join(repo.Location, "config")
	if _, err := os.Stat(cfg); err == nil {
		return nil
	}
	if err := os.MkdirAll(repo.Location, 0o700); err != nil {
		return err
	}
	return r.Run(ctx, repo, nil, io.Discard, "init", "--repository-version", "2")
}

func (r Runner) Put(ctx context.Context, repo Repo, name string, body io.Reader) (string, error) {
	var stdout bytes.Buffer
	err := r.Run(ctx, repo, body, &stdout, "backup", "--stdin", "--stdin-filename", name, "--tag", "weazlcloud", "--json")
	if err != nil {
		return "", err
	}
	return snapshotID(stdout.Bytes())
}

func (r Runner) PutBatch(ctx context.Context, repo Repo, root string) (string, error) {
	var stdout bytes.Buffer
	err := r.Run(ctx, repo, nil, &stdout, "backup", "--no-scan", "--tag", "weazlcloud", "--json", root)
	if err != nil {
		return "", err
	}
	return snapshotID(stdout.Bytes())
}

func (r Runner) Dump(ctx context.Context, repo Repo, snap, name string, w io.Writer) error {
	return r.Run(ctx, repo, nil, w, "dump", snap, name)
}

func (r Runner) Snapshots(ctx context.Context, repo Repo) ([]string, error) {
	var raw []struct {
		ID string `json:"id"`
	}
	var out bytes.Buffer
	if err := r.Run(ctx, repo, nil, &out, "snapshots", "--json"); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(raw))
	for _, snapshot := range raw {
		if snapshot.ID != "" {
			ids = append(ids, snapshot.ID)
		}
	}
	return ids, nil
}

func (r Runner) Forget(ctx context.Context, repo Repo, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"forget", "--prune"}, ids...)
	return r.Run(ctx, repo, nil, io.Discard, args...)
}

func snapshotID(stdout []byte) (string, error) {
	var id string
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Type string `json:"message_type"`
			ID   string `json:"snapshot_id"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if msg.Type == "summary" && msg.ID != "" {
			id = msg.ID
		}
	}
	if id == "" {
		return "", errors.New("library put did not finish")
	}
	return id, nil
}
