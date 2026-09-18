package library

import (
	"errors"
	"path"
	"strings"
)

var ErrBadPath = errors.New("path is not allowed")

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
