package takeout

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

type DirectoryRename struct {
	Original string `json:"original"`
	Path     string `json:"path"`
	Variant  int    `json:"variant,omitempty"`
}

func folderConflictPath(name string, variant int) string {
	digest := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf("takeout-folder-%x", digest[:16])
	if variant > 0 {
		suffix += fmt.Sprintf("-%d", variant+1)
	}
	return name + " (" + suffix + ")"
}

// Resolve ancestors before admitting a wave. A file occupying a directory
// destination stays intact; incoming descendants use a deterministic sibling.
func resolveFolders(ctx context.Context, lib *library.Library, existing map[string]catalog.File, target string, folder bool) (string, []DirectoryRename, error) {
	parts := strings.Split(target, "/")
	count := len(parts) - 1
	if folder {
		count++
	}
	resolved := make([]string, 0, len(parts))
	var renamed []DirectoryRename
	for i, part := range parts {
		resolved = append(resolved, part)
		if i >= count {
			break
		}
		name := strings.Join(resolved, "/")
		if f, exists := existing[name]; exists && !f.Folder {
			for variant := 0; ; variant++ {
				candidate := folderConflictPath(name, variant)
				f, exists = existing[candidate]
				if exists && !f.Folder {
					continue
				}
				renamed = append(renamed, DirectoryRename{Original: strings.Join(parts[:i+1], "/"), Path: candidate, Variant: variant})
				resolved[i] = strings.TrimPrefix(candidate, strings.Join(resolved[:i], "/")+"/")
				if i == 0 {
					resolved[i] = candidate
				}
				name = candidate
				break
			}
		}
		if err := ensureFolder(ctx, lib, existing, name); err != nil {
			return "", nil, err
		}
	}
	return strings.Join(resolved, "/"), renamed, nil
}
