package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var errBatch = errors.New("batch write failed")

type entry struct {
	Name   string `json:"name"`
	Size   *int64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version int     `json:"version"`
	Files   []entry `json:"files"`
}

func lowerHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func readManifest(r io.Reader) (manifest, error) {
	var m manifest
	b, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil || len(b) > 64<<10 {
		return m, errBatch
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.Version != 1 || len(m.Files) < 1 || len(m.Files) > 8 {
		return m, errBatch
	}
	seen := make(map[string]bool)
	var total int64
	for _, f := range m.Files {
		if !lowerHex(f.Name, 32) || seen[f.Name] || f.Size == nil || *f.Size < 0 || !lowerHex(f.SHA256, 64) {
			return m, errBatch
		}
		if *f.Size > (64<<20)-total {
			return m, errBatch
		}
		total += *f.Size
		seen[f.Name] = true
	}
	return m, nil
}
