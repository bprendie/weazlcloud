package library

import (
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/catalog"
)

// One small encrypted segment per immutable asset. It contains no plaintext
// path and never replaces the authenticated variant file as proof of readiness.
type previewManifest struct {
	Version   int    `json:"version"`
	Renderer  string `json:"renderer"`
	Grid      string `json:"grid"`
	Viewer    string `json:"viewer"`
	ThumbHash []byte `json:"thumbhash,omitempty"`
}

func (l *Library) writePreviewManifest(f catalog.File, hash []byte) error {
	if len(hash) < 5 || len(hash) > 128 {
		return nil
	}
	key, err := thumbnailKey(l.vault, f, 0)
	if err != nil {
		return err
	}
	grid, err := thumbnailKey(l.vault, f, 320)
	if err != nil {
		return err
	}
	viewer, err := thumbnailKey(l.vault, f, 1280)
	if err != nil {
		return err
	}
	body, err := json.Marshal(previewManifest{1, thumbnailRenderer, grid, viewer, hash})
	if err != nil {
		return err
	}
	return l.writeThumbnailCache(key+".meta", thumbnailEnvelope{ContentType: "application/vnd.weazl.preview-manifest", Body: body, Size: 320})
}
func (l *Library) photoPreviewHint(f catalog.File) (string, []byte) {
	key, err := thumbnailKey(l.vault, f, 0)
	if err != nil {
		return "", nil
	}
	env, ok := l.readThumbnailEnvelope(key + ".meta")
	if !ok || len(env.Body) > 4096 {
		return key, nil
	}
	var manifest previewManifest
	if json.Unmarshal(env.Body, &manifest) != nil || manifest.Version != 1 || manifest.Renderer != thumbnailRenderer || len(manifest.ThumbHash) > 128 {
		return key, nil
	}
	return key, manifest.ThumbHash
}
