package library

import (
	"errors"
	"path"
	"strings"
)

var ErrBadPath = errors.New("path is not allowed")

// CleanPath validates a user supplied library path for services that need to
// persist work before handing it to the library.
func CleanPath(p string) (string, error) { return cleanPath(p) }

func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.Contains(p, "\x00") {
		return "", ErrBadPath
	}
	if path.IsAbs(p) {
		return "", ErrBadPath
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrBadPath
	}
	for _, part := range strings.Split(c, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrBadPath
		}
	}
	return c, nil
}
