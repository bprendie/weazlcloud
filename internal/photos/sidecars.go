package photos

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

var ErrAmbiguousSidecar = errors.New("ambiguous Takeout sidecar")

// SidecarIndex is directory-local. Titles, never prefixes alone, validate
// truncated and collision names. An exact conventional name wins.
type SidecarIndex struct {
	Exact  map[string][]string
	Titles map[string][]string
}

func NewSidecarIndex() SidecarIndex {
	return SidecarIndex{Exact: map[string][]string{}, Titles: map[string][]string{}}
}
func (index *SidecarIndex) Add(name string, raw []byte) error {
	var doc struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	base := path.Base(name)
	if strings.HasSuffix(base, ".supplemental-metadata.json") {
		key := strings.TrimSuffix(base, ".supplemental-metadata.json")
		index.Exact[key] = append(index.Exact[key], name)
	} else if strings.HasSuffix(base, ".json") {
		key := strings.TrimSuffix(base, ".json")
		index.Exact[key] = append(index.Exact[key], name)
	}
	if doc.Title != "" && path.Base(doc.Title) == doc.Title && !strings.ContainsAny(doc.Title, "\\\x00") {
		index.Titles[doc.Title] = append(index.Titles[doc.Title], name)
	}
	return nil
}
func (index SidecarIndex) Match(name string) (string, error) {
	candidates := index.Exact[name]
	if len(candidates) == 0 {
		candidates = index.Titles[name]
	}
	if len(candidates) > 1 {
		return "", ErrAmbiguousSidecar
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", ErrNoCaptureMetadata
}
