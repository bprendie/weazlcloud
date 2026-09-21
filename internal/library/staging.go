package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/restic"
)

type stagedUpload struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Data      string    `json:"data"`
	Size      int64     `json:"size"`
	Expected  int64     `json:"expected"`
	Hash      string    `json:"hash"`
	Snap      string    `json:"snap,omitempty"`
	Object    string    `json:"object,omitempty"`
	BatchRoot string    `json:"batch_root,omitempty"`
	Mtime     time.Time `json:"mtime"`
}

func (l *Library) stageReader(name string, body io.Reader, expected int64) (stagedUpload, error) {
	if err := os.MkdirAll(l.stageDir(), 0o700); err != nil {
		return stagedUpload{}, err
	}
	idBytes, err := cryptox.Random(16)
	if err != nil {
		return stagedUpload{}, err
	}
	id := hex.EncodeToString(idBytes)
	stage := stagedUpload{ID: id, Path: name, Data: id + ".data", Expected: expected, Mtime: time.Now().UTC()}
	l.setStageActive(id, true)
	dataPath := filepath.Join(l.stageDir(), stage.Data)
	tmp, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		l.setStageActive(id, false)
		return stagedUpload{}, err
	}
	h := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, h), body)
	if copyErr == nil && expected >= 0 && size != expected {
		copyErr = errors.New("upload size changed while reading")
	}
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(dataPath)
		l.setStageActive(id, false)
		return stagedUpload{}, copyErr
	}
	stage.Size = size
	stage.Hash = hex.EncodeToString(h.Sum(nil))
	if err := l.writeStage(stage); err != nil {
		_ = os.Remove(dataPath)
		l.setStageActive(id, false)
		return stagedUpload{}, err
	}
	return stage, nil
}

func (l *Library) commitStaged(ctx context.Context, stage stagedUpload) (catalog.File, error) {
	if stage.Snap == "" {
		dataPath := filepath.Join(l.stageDir(), stage.Data)
		tmp, err := os.Open(dataPath)
		if err != nil {
			return catalog.File{}, err
		}
		pass, _, err := l.vault.Secrets()
		if err == nil {
			stage.Snap, err = l.restic.Put(ctx, restic.Repo{Location: l.repo, Password: pass}, stage.Hash, tmp)
		}
		_ = tmp.Close()
		if err != nil {
			return catalog.File{}, err
		}
		l.resticCommits.Add(1)
		stage.Object = stage.Hash
		if err := l.writeStage(stage); err != nil {
			return catalog.File{}, err
		}
	}
	if stage.Object == "" {
		stage.Object = stage.Hash
	}
	f := catalog.File{Path: stage.Path, Size: stage.Size, Mtime: stage.Mtime, Hash: stage.Hash, Snap: stage.Snap, Object: stage.Object, Present: true}
	if err := l.catalog.Put(f); err != nil {
		return catalog.File{}, err
	}
	if err := l.removeStage(stage); err != nil {
		return catalog.File{}, err
	}
	l.cleanupBatchRoot(stage.BatchRoot)
	return f, nil
}

func (l *Library) recoverStaged(ctx context.Context) error {
	entries, err := os.ReadDir(l.stageDir())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stage, err := l.readStage(entry.Name())
		if err != nil {
			return fmt.Errorf("read staged upload %s: %w", entry.Name(), err)
		}
		if l.stageActive(stage.ID) {
			continue
		}
		if _, err := cleanPath(stage.Path); err != nil || stage.ID == "" || stage.Data == "" || stage.Hash == "" {
			return fmt.Errorf("invalid staged upload %s", entry.Name())
		}
		if existing, ok := l.catalog.Get(stage.Path); ok && existing.Hash != stage.Hash {
			// A later successful overwrite won the race before this process
			// recovered. The stale plaintext is no longer needed.
			if err := l.removeStage(stage); err != nil {
				return err
			}
			continue
		}
		if _, err := l.commitStaged(ctx, stage); err != nil {
			return fmt.Errorf("recover staged upload %s: %w", stage.Path, err)
		}
	}
	for _, root := range l.batchRoots() {
		l.cleanupBatchRoot(root)
	}
	l.cleanupOrphanStageData(entries)
	return nil
}

func (l *Library) batchRoots() []string {
	roots, err := filepath.Glob(filepath.Join(filepath.Dir(l.repo), ".weazl-batch-*"))
	if err != nil {
		return nil
	}
	return roots
}

func (l *Library) cleanupOrphanStageData(entries []os.DirEntry) {
	referenced := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			if stage, err := l.readStage(entry.Name()); err == nil {
				referenced[stage.Data] = true
			}
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".data") || referenced[entry.Name()] || l.stageActive(strings.TrimSuffix(entry.Name(), ".data")) {
			continue
		}
		_ = os.Remove(filepath.Join(l.stageDir(), entry.Name()))
	}
}

func (l *Library) stageDir() string { return filepath.Join(l.repo, ".staging") }

func (l *Library) stagePath(id string) string { return filepath.Join(l.stageDir(), id+".json") }

func (l *Library) writeStage(stage stagedUpload) error {
	b, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(l.stagePath(stage.ID), append(b, '\n'), 0o600)
}

func (l *Library) readStage(name string) (stagedUpload, error) {
	var stage stagedUpload
	b, err := os.ReadFile(filepath.Join(l.stageDir(), name))
	if err != nil {
		return stage, err
	}
	err = json.Unmarshal(b, &stage)
	return stage, err
}

func (l *Library) removeStage(stage stagedUpload) error {
	if err := os.Remove(filepath.Join(l.stageDir(), stage.Data)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(l.stagePath(stage.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Library) cleanupBatchRoot(root string) {
	if root == "" {
		return
	}
	entries, err := os.ReadDir(l.stageDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stage, err := l.readStage(entry.Name())
		if err == nil && stage.BatchRoot == root {
			return
		}
	}
	_ = os.RemoveAll(root)
}

func (l *Library) setStageActive(id string, active bool) {
	l.stageMu.Lock()
	defer l.stageMu.Unlock()
	if active {
		l.activeStages[id] = struct{}{}
	} else {
		delete(l.activeStages, id)
	}
}

func (l *Library) stageActive(id string) bool {
	l.stageMu.Lock()
	defer l.stageMu.Unlock()
	_, ok := l.activeStages[id]
	return ok
}
