package takeout

import (
	"errors"
	"strings"

	"github.com/bprendie/weazlcloud/internal/library"
)

// An empty prefix imports Drive into the library root and Photos into Photos.
// Nonempty prefixes retain the original grouped layout for explicit callers.
func Destination(prefix, raw string) (string, error) {
	clean, err := cleanEntry(raw)
	if err != nil {
		return "", err
	}
	parts := strings.Split(clean, "/")
	if len(parts) > 1 && strings.EqualFold(parts[0], "Takeout") {
		parts = parts[1:]
	}
	group := "Other"
	switch strings.ToLower(parts[0]) {
	case "drive", "google drive":
		group = "Drive"
		parts = parts[1:]
	case "photos", "google photos":
		group = "Photos"
		parts = parts[1:]
	}
	var root []string
	if prefix != "" {
		if _, err := library.CleanPath(prefix); err != nil {
			return "", err
		}
		root = []string{prefix, group}
	} else if group != "Drive" {
		root = []string{group}
	}
	target := strings.Join(append(root, parts...), "/")
	if target == "" {
		if strings.HasSuffix(raw, "/") {
			return "", nil
		}
		return "", errors.New("file cannot replace the library root")
	}
	return library.CleanPath(target)
}

// LayoutPath only transforms the legacy import namespace. Storage object
// names and Restic snapshots are deliberately not part of this mapping.
func LayoutPath(old string) (string, error) {
	if old == "Google Takeout" || old == "Google Takeout/Drive" {
		return "", nil
	}
	if !strings.HasPrefix(old, "Google Takeout/") {
		return old, nil
	}
	rel := strings.TrimPrefix(old, "Google Takeout/")
	if strings.HasPrefix(rel, "Other/") {
		return cleanEntry(rel)
	}
	return Destination("", rel)
}
